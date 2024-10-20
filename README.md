# Podman service to HTTP Traefik provider

A simple Golang script that lists services from Podman and converts them into Traefik services and router configurations. This tool generates a dynamic Traefik configuration from running Podman containers, allowing seamless integration between Podman and Traefik.

Useful for having a single traefik instance and services running into multiple instances without having to configure each service manually on a file provider

NOTE: YOU SHOULD NOT USE THIS IN PRODUCTION ENVIRONMENTS

## Features

- Lists running services in Podman that has traefik annocations
  - it requires `traefik.enabled=true`
- Converts Podman services into Traefik service and router configurations.

## Requirements

- Go 1.23 or higher
- Podman installed on your system
- Traefik for managing reverse proxies

## Installation

Clone the repository:

```bash
git clone https://github.com/marcelohpf/podman-http-traefik.git
cd podman-http-traefik
CGO_ENABLED=0 go build -tags 'containers_image_openpgp'
```

## Usage

PTOT_IP=192.168.0.2 ./podman-http-traefik
