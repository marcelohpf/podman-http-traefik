package main

//CGO_ENABLED=0 go build -tags 'containers_image_openpgp'

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
	"github.com/traefik/traefik/v3/pkg/config/label"
	"github.com/traefik/traefik/v3/pkg/provider"
)

// Function to get Traefik configuration from Docker containers
func getTraefikConfig() (*dynamic.Configuration, error) {
	// Get Podman socket location
	sock_path := os.Getenv("PTOC_SOCKET")
  if sock_path == "" {
    sock_path = "/run/user/1000/podman/podman.sock"
  }
	socket := "unix://" + sock_path
	// Connect to Podman socket
  connText, err := bindings.NewConnection(context.Background(), socket)
  if err != nil {
    return nil, fmt.Errorf("unable to list containers: %v", err)
  }

  ip := os.Getenv("PTOT_IP")
  // Container list
  containers, err := containers.List(connText, &containers.ListOptions{Filters: map[string][]string{"label": []string{ "traefik.enabled=true"} }})
  if err != nil {
    return nil, fmt.Errorf("unable to list containers: %v", err)
  }

  configs := dynamic.Configurations{}
	for _, container := range containers {
    labels := container.Labels
    config, err := label.DecodeConfiguration(labels)
    if err != nil {
      log.Printf("skipping container %s: %v", container.ID, err)
      continue
    }

    for routerName, router := range config.HTTP.Routers {
      port, ok := labels[fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", routerName)]
      if !ok {
        log.Printf("missing traefik.http.services.%s.loadbalander.server.port for %s", routerName, container.ID)
        continue
      }
      bindingIp := ip
      for _, portMapping := range container.Ports {
        if strconv.FormatUint(uint64(portMapping.HostPort), 10) == port && portMapping.HostIP != "0.0.0.0" || portMapping.HostIP != "" {
          bindingIp = portMapping.HostIP
        }
      }
      serviceName := router.Service
      if serviceName == "" {
        serviceName = routerName
        router.Service = routerName
      }
      service, ok := config.HTTP.Services[serviceName]
      if !ok {
        service = &dynamic.Service{
          LoadBalancer: &dynamic.ServersLoadBalancer{
            Servers: []dynamic.Server{
              {URL: fmt.Sprintf("http://%s:%s", bindingIp, port)},
            },
          },
        }
      }
      if service.LoadBalancer.Servers[0].URL == "" {
        service.LoadBalancer.Servers[0].URL = fmt.Sprintf("%s://%s:%s", service.LoadBalancer.Servers[0].Scheme, bindingIp, service.LoadBalancer.Servers[0].Port)
      } else {
        service.LoadBalancer.Servers = append(service.LoadBalancer.Servers, dynamic.Server{
          URL: fmt.Sprintf("http://%s:%s", bindingIp, port),})
        }

      config.HTTP.Services[serviceName] = service

    }
    configs[container.ID] = config
	}
	return provider.Merge(context.TODO(), configs), nil
}

// HTTP handler for Traefik configuration endpoint
func traefikHandler(w http.ResponseWriter, r *http.Request) {
	traefikConfig, err := getTraefikConfig()
	if err != nil {
		http.Error(w, "Failed to get Traefik config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Set content type to JSON
	w.Header().Set("Content-Type", "application/json")

  configs := map[string]interface{}{}
  if len(traefikConfig.HTTP.Services) != 0 || len(traefikConfig.HTTP.Routers) != 0 {
    configs["http"]= traefikConfig.HTTP
  }
  if len(traefikConfig.TCP.Routers) != 0 || len(traefikConfig.TCP.Services) != 0 {
    configs["tcp"]= traefikConfig.TCP
  }
  if len(traefikConfig.UDP.Routers) != 0 || len(traefikConfig.UDP.Services) != 0 {
    configs["udp"]= traefikConfig.UDP
  }
	// Convert Traefik configuration to JSON and write it to the response
	json.NewEncoder(w).Encode(configs)
}

func main() {
	// Expose an API at /traefik/config
	http.HandleFunc("/traefik/config", traefikHandler)

	// Start the HTTP server on port 5000
	port := "5000"
	if fromEnv := os.Getenv("PTOC_PORT"); fromEnv != "" {
		port = fromEnv
	}

	log.Printf("Starting server on port %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
