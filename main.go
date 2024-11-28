package main

//CGO_ENABLED=0 go build -tags 'containers_image_openpgp'

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
	"github.com/traefik/traefik/v3/pkg/config/label"
	"github.com/traefik/traefik/v3/pkg/provider"
)

type Config struct {
	Socket   string
	Ip       string
	Host     string
	Port     string
	Loglevel string
}

// Function to get Traefik configuration from Docker containers
func getTraefikConfig(c Config) (*dynamic.Configuration, error) {
	// Get Podman socket location
	configs := dynamic.Configurations{}

	socket := "unix://" + c.Socket
	log.Tracef("Listing containers from %s", socket)
	// Connect to Podman socket
	connText, err := bindings.NewConnection(context.Background(), socket)
	if err != nil {
		return nil, fmt.Errorf("unable to list containers: %v", err)
	}

	// Container list
	containers, err := containers.List(connText, &containers.ListOptions{Filters: map[string][]string{"label": []string{"traefik.enable=true"}}})
	if err != nil {
		return nil, fmt.Errorf("unable to list containers: %v", err)
	}
	log.Tracef("Found %d containers with label traefik.enable=true", len(containers))

	for _, container := range containers {
		log.Tracef("Processing container %s %v", strings.Join(container.Names, "/"), container.Labels)
		labels := container.Labels
		config, err := label.DecodeConfiguration(labels)
		if err != nil {
			log.Errorf("skipping container %s: %v", container.ID, err)
			continue
		}

		for routerName, router := range config.HTTP.Routers {
			port, ok := labels[fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", routerName)]
			if !ok {
				log.Warnf("Missing traefik.http.services.%s.loadbalander.server.port for %s", routerName, container.ID)
				continue
			}
			bindingIp := c.Ip
			for _, portMapping := range container.Ports {
				if strconv.FormatUint(uint64(portMapping.HostPort), 10) == port && portMapping.HostIP != "0.0.0.0" && portMapping.HostIP != "" {
					log.WithFields(log.Fields{"container": container.ID, "router": routerName}).Debugf("Using HostIP from port mapping %s", portMapping.HostIP)
					bindingIp = portMapping.HostIP
				}
			}
			serviceName := router.Service
			if serviceName == "" {
				serviceName = routerName
				router.Service = routerName
			}
			service, ok := config.HTTP.Services[serviceName]
			url := fmt.Sprintf("http://%s:%s", bindingIp, port)
			if !ok {
				service = &dynamic.Service{
					LoadBalancer: &dynamic.ServersLoadBalancer{
						Servers: []dynamic.Server{
							{URL: url},
						},
					},
				}
			}

			if service.LoadBalancer.Servers[0].URL == "" {
				url = fmt.Sprintf("%s://%s:%s", service.LoadBalancer.Servers[0].Scheme, bindingIp, service.LoadBalancer.Servers[0].Port)
				service.LoadBalancer.Servers[0].URL = url
			} else {
				service.LoadBalancer.Servers = append(service.LoadBalancer.Servers, dynamic.Server{
					URL: url})
			}
			log.WithFields(log.Fields{
				"container": container.ID,
				"service":   serviceName,
				"route":     routerName,
			}).Debugf("http service added %s", url)
			config.HTTP.Services[serviceName] = service
		}
		configs[container.ID] = config
	}

	return provider.Merge(context.TODO(), configs), nil
}

// HTTP handler for Traefik configuration endpoint
func (s server) traefikHandler(w http.ResponseWriter, r *http.Request) {
	traefikConfig, err := getTraefikConfig(s.c)
	if err != nil {
		http.Error(w, "Failed to get Traefik config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Set content type to JSON
	w.Header().Set("Content-Type", "application/json")

	configs := map[string]interface{}{}
	if len(traefikConfig.HTTP.Services) != 0 || len(traefikConfig.HTTP.Routers) != 0 {
		configs["http"] = traefikConfig.HTTP
	}
	if len(traefikConfig.TCP.Routers) != 0 || len(traefikConfig.TCP.Services) != 0 {
		configs["tcp"] = traefikConfig.TCP
	}
	if len(traefikConfig.UDP.Routers) != 0 || len(traefikConfig.UDP.Services) != 0 {
		configs["udp"] = traefikConfig.UDP
	}

	// Convert Traefik configuration to JSON and write it to the response
	json.NewEncoder(w).Encode(configs)
}

func (s server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ok := true

	_, err := os.Stat(s.c.Socket)
	if err != nil {
		log.Errorf("socket not available: %v", err)
		ok = false
	}

	body, err := json.Marshal(map[string]interface{}{
		"ok": ok,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Errorf("unable to encode response body: %v", err)
		return
	}

	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
	}
	w.Write(body)
}

func loadConfig() (Config, error) {
	sockPath := os.Getenv("PTOT_SOCKET")
	if sockPath == "" {
		runUser, err := user.Current()
		if err != nil {
			return Config{}, err
		}
		sockPath = fmt.Sprintf("/run/user/%s/podman/podman.sock", runUser.Uid)
	}
	host := os.Getenv("PTOT_HOST")
	if host == "" {
		host = "0.0.0.0"
	}

	ip := os.Getenv("PTOT_IP")
	if ip == "" {
		ip = "127.0.0.1"
	}

	port := os.Getenv("PTOT_PORT")
	if port == "" {
		port = "5000"
	}

	logLevel := os.Getenv("PTOT_LOG")
	switch logLevel {
	case "panic", "fatal", "error", "warn", "info", "debug", "trace":
		// ok
	case "":
		logLevel = "info"
	default:
		return Config{}, fmt.Errorf("invalid log level %s", logLevel)
	}
	return Config{
		Socket:   sockPath,
		Ip:       ip,
		Host:     host,
		Port:     port,
		Loglevel: logLevel,
	}, nil
}

func setupLog(c Config) error {
	log.SetFormatter(&log.JSONFormatter{})
	logLevel, err := log.ParseLevel(c.Loglevel)
	if err != nil {
		return err
	}
	log.SetLevel(logLevel)
	return nil
}

type server struct {
	c Config
}

func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Could not parse configs: %v", err)
		return
	}
	if err := setupLog(config); err != nil {
		log.Fatalf("Failed setting up log: %v", err)
		return
	}

	s := server{c: config}

	// Expose an API at /traefik/config
	http.HandleFunc("/traefik/config", s.traefikHandler)
	http.HandleFunc("/healthcheck", s.health)

	// Start the HTTP server on port 5000

	log.Infof("Starting server on port %s...", config.Port)
	if err := http.ListenAndServe(fmt.Sprintf("%s:%s", config.Host, config.Port), nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
