# arca-lb Installation Guide

This document explains how to install arca-lb.

## Prerequisites

### Controller

- **Go**: 1.23+ (for builds)
- **MySQL**: 8.0+ (optional; not needed if you use etcd)
- **etcd**: 3.5+ (optional; not needed if you use MySQL)
- **Docker**: 20.10+ (optional; when running in containers)

### Agent

- **Go**: 1.23+ (for builds)
- **VPP**: 22.02+ (runtime)
- **FRRouting**: 8.0+ (runtime, required for BGP advertisements)
- **Docker**: 20.10+ (optional; when running in containers)

## Installation Methods

### Method 1: Build binaries

#### 1. Clone the repository

```bash
git clone https://github.com/akam1o/arca-lb.git
cd arca-lb
```

#### 2. Install dependencies

```bash
make deps
```

#### 3. Build

```bash
make build
```

After a successful build, the following binaries are created in `bin/`:

- `bin/arcalb-controller` - Controller binary
- `bin/arcalb-agent` - Agent binary

### Method 2: Build Docker images

#### 1. Build Docker images

```bash
make docker
```

Or build individually:

```bash
docker build -f deploy/docker/Dockerfile.controller -t arcalb-controller:latest .
docker build -f deploy/docker/Dockerfile.agent -t arcalb-agent:latest .
```

#### 2. Start with Docker Compose

```bash
cd deploy/docker-compose
docker compose -f docker-compose.dev.yml up -d
```

### Method 3: Deploy to Kubernetes

#### 1. Create a namespace

```bash
kubectl create namespace arca-lb
```

#### 2. Deploy the Controller

```bash
kubectl apply -f deploy/kubernetes/controller-deployment.yaml
```

#### 3. Deploy the Agent

```bash
kubectl apply -f deploy/kubernetes/agent-daemonset.yaml
```

#### 4. Deploy vpp-exporter (optional)

```bash
kubectl apply -f deploy/kubernetes/vpp-exporter-daemonset.yaml
```

## Initial Configuration

### Controller configuration

1. Copy the example config

```bash
cp deploy/config/controller.example.yaml deploy/config/controller.yaml
```

2. Edit the config

Update `deploy/config/controller.yaml` with your datastore (MySQL or etcd) connection details.

### Agent configuration

1. Copy the example config

```bash
cp deploy/config/agent.example.yaml deploy/config/agent.yaml
```

2. Edit the config

Update `deploy/config/agent.yaml` with the Controller gRPC endpoint and VPP settings.

## Start the Services

### Start the Controller

```bash
./bin/arcalb-controller --config deploy/config/controller.yaml
```

Or with Docker:

```bash
docker run -d \
  --name arcalb-controller \
  -v $(pwd)/deploy/config/controller.yaml:/app/config/controller.yaml:ro \
  -p 8080:8080 \
  -p 50051:50051 \
  arcalb-controller:latest
```

### Start the Agent

```bash
# Set the config path via environment variable
export ARCA_AGENT_CONFIG=deploy/config/agent.yaml
sudo ./bin/arcalb-agent
```

**Note**: The Agent may need `sudo` to access the VPP socket. It reads the config path from the `ARCA_AGENT_CONFIG` environment variable, not from a `--config` flag.

Or with Docker (host network mode):

```bash
docker run -d \
  --name arcalb-agent \
  --privileged \
  --network host \
  -v /run/vpp/api.sock:/run/vpp/api.sock:ro \
  -v /run/vpp/stats.sock:/run/vpp/stats.sock:ro \
  -v $(pwd)/deploy/config/agent.yaml:/app/config/agent.yaml:ro \
  arcalb-agent:latest
```

## Verification

### Check the Controller

```bash
curl http://localhost:8080/healthz
```

A healthy response looks like:

```json
{
  "status": "healthy",
  "time": "2025-12-20T10:00:00Z"
}
```

### Check the Agent

Inspect the Agent logs to confirm it started successfully:

```bash
# Verify there are no errors in the logs
# If metrics are enabled (metrics.enabled: true)
curl http://localhost:9090/metrics
```

**Note**: Metrics are disabled by default (`metrics.enabled: false`). To enable them, set `metrics.enabled: true` in the config file.

## Next Steps

- See the [Configuration Guide](./configuration.md) for detailed settings
- See the [REST API Reference](./api.md) for API usage
