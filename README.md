# arca-lb

**arca-lb** is a centralized control plane for VPP-based Layer 4 load balancing, designed for environments that demand line-rate performance, operational simplicity, and horizontal scalability.

## Docker Images

- Controller: https://hub.docker.com/r/akam1o/arca-lb-controller
- Agent: https://hub.docker.com/r/akam1o/arca-lb-agent

## Features

- **Centralized management**: Unified VIP and backend management via REST API
- **High-performance data plane**: Fast packet processing powered by the VPP L4 LB plugin
- **Flexible health checks**: Supports HTTP/HTTPS, TCP, and Ping probes
- **Automatic route announcements**: BGP advertisements through FRR integration
- **Scalable**: Can be distributed across multiple Agents

## Architecture

```
┌─────────────────────────────────────────┐
│             REST API Client             │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│      Controller (REST + gRPC)           │
│  - VIP/Backend management               │
│  - Datastore (etcd/MySQL)               │
│  - Config delivery to Agents            │
└────────────┬────────────────────────────┘
             │ gRPC
             ▼
┌─────────────────────────────────────────┐
│      Agent (per load balancer node)     │
│  - VPP L4 LB control                    │
│  - Health checks                        │
│  - FRR BGP route announcements          │
└─────────────────────────────────────────┘
```

## Project Structure

```
arca-lb/
├── cmd/                    # Entry points
│   ├── arcalb-controller/  # Controller binary
│   └── arcalb-agent/       # Agent binary
├── internal/               # Internal packages
│   ├── controller/         # Controller implementation
│   ├── agent/              # Agent implementation
│   └── common/             # Shared packages
├── pkg/                    # Public APIs
├── api/                    # API definitions
│   ├── proto/              # gRPC Protocol Buffers
│   └── openapi/            # REST API OpenAPI
├── deploy/                 # Deployment artifacts
│   ├── docker/             # Dockerfiles
│   ├── docker-compose/     # Docker Compose
│   └── kubernetes/         # Kubernetes manifests
├── test/                   # Tests
├── docs/                   # Documentation
└── migrations/             # Database migrations
```

## Requirements

- **Go**: 1.24+ (development)
- **VPP**: 24.10 (recommended, Agent runtime)
- **FRRouting**: 8.0+ (Agent runtime)
- **etcd**: 3.5+ (Controller, optional)
- **MySQL**: 8.0+ (Controller, optional)
- **Docker**: 20.10+ (optional)

## Quickstart

### Set Up the Development Environment

1. Clone the repository
```bash
git clone https://github.com/akam1o/arca-lb.git
cd arca-lb
```

2. Install dependencies
```bash
make deps
```

3. Prepare configuration files
```bash
cp deploy/config/controller.example.yaml deploy/config/controller.yaml
cp deploy/config/agent.example.yaml deploy/config/agent.yaml
```

4. Start a datastore (Docker Compose)
```bash
docker compose -f deploy/docker-compose/docker-compose.dev.yml up -d etcd
# or
docker compose -f deploy/docker-compose/docker-compose.dev.yml up -d mysql
```

5. Build
```bash
make build
```

### Start the Controller

```bash
./bin/arcalb-controller --config deploy/config/controller.yaml
```

### Start the Agent

```bash
export ARCA_AGENT_CONFIG=deploy/config/agent.yaml
sudo ./bin/arcalb-agent
```

**Note**: The Agent reads the config path from the `ARCA_AGENT_CONFIG` environment variable (default: `/etc/arca-lb/agent.yaml`), not from a `--config` flag.

## Makefile Targets

```bash
make help          # Show available targets
make deps          # Download dependencies
make build         # Build binaries
make test          # Run tests
make lint          # Run linters
make proto         # Generate Protocol Buffers code
make docker        # Build Docker images
make clean         # Remove build artifacts
```

## Documentation

For detailed documentation, see the `docs/` directory:

### Operations
- [Installation Guide](docs/installation.md) - Installation steps and setup
- [Configuration Guide](docs/configuration.md) - How to configure the Controller and Agent
- [API Reference](docs/api.md) - REST API reference
- [Troubleshooting](docs/troubleshooting.md) - Common issues and fixes
- [Backend Server Setup Guide](docs/backend-setup.md) - How to configure backend servers

### Developer Docs
- [Architecture](docs/architecture.md) - System architecture and design
- [Development Environment](docs/development.md) - Dev environment setup and workflow
- [Contribution Guide](docs/contributing.md) - How to contribute to the project

## Contact

For inquiries, email `arca-projects@ark-networks.net` or open an issue on GitHub.

## License

Apache License 2.0
