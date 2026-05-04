# arca-lb

**arca-lb** is a Kubernetes-native control plane for VPP-based Layer 4 load balancing, designed for environments that demand line-rate performance, operational simplicity, and horizontal scalability.

## Docker Images

- Operator: `ghcr.io/akam1o/arca-lb-operator`
- Agent: `ghcr.io/akam1o/arca-lb-agent`

## Features

- **Kubernetes-native**: Declarative VIP management via `VirtualIP` Custom Resource (CRD)
- **Operator pattern**: Kubernetes Operator handles validation, status, and lifecycle
- **High-performance data plane**: Wire-rate packet processing powered by the VPP L4 LB plugin
- **Pluggable interfaces**: DataPlane and Router interfaces for testability and extension
- **Flexible health checks**: Supports HTTP/HTTPS, TCP, Ping, and TLS hello probes with per-VIP configuration
- **Automatic route announcements**: BGP advertisements through FRR integration
- **Scalable**: One Agent per LB node, deployed as a DaemonSet
- **Observable**: OpenTelemetry traces/metrics, Prometheus endpoint, structured logging
- **OpenStack Octavia**: Provider driver for integration with OpenStack LBaaS API

## Architecture

```
┌─────────────────────────────────────────┐
│           kubectl / GitOps              │
│   (apply VirtualIP CRD manifests)       │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│      Kubernetes API Server              │
│  - VirtualIP CRD (arca.io/v1alpha1)     │
│  - CRD admission validation             │
└──────┬──────────────────────┬───────────┘
       │                      │
       ▼                      ▼
┌──────────────┐   ┌──────────────────────┐
│   Operator   │   │  Agent (per LB node) │
│  - Reconcile │   │  - K8s Informer      │
│  - Status    │   │  - Per-VIP Reconciler │
│  - Validation│   │  - Health Checks     │
└──────────────┘   │  - VPP DataPlane     │
                   │  - FRR Router        │
                   │  - bbolt Local Store  │
                   │  - OTel Telemetry    │
                   └──────────────────────┘
```

### VirtualIP Custom Resource Example

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: web-vip
spec:
  address: 203.0.113.10
  port: 80
  protocol: TCP
  encapType: L3DSR
  dscp: 10
  backends:
    - address: 10.0.1.1
      weight: 1
    - address: 10.0.1.2
      weight: 1
  healthCheck:
    type: http
    intervalSeconds: 5
    timeoutSeconds: 3
    riseCount: 3
    fallCount: 2
    http:
      port: 8080
      path: /healthz
```

## Project Structure

```
arca-lb/
├── api/
│   └── v1alpha1/           # VirtualIP CRD types (kubebuilder)
├── cmd/
│   ├── operator/           # Operator (K8s controller) binary
│   └── arcalb-agent/       # Agent binary
├── config/                 # K8s manifests (generated + hand-written)
│   ├── crd/                # CRD YAML (controller-gen output)
│   ├── rbac/               # RBAC roles
│   ├── manager/            # Operator Deployment
│   ├── agent/              # Agent DaemonSet
│   └── samples/            # Example VirtualIP resources
├── internal/
│   ├── operator/           # Operator reconciler + optional webhook
│   ├── agent/              # Agent implementation
│   │   ├── config/         # Agent configuration
│   │   ├── dataplane/      # DataPlane interface (VPP, Noop)
│   │   ├── routing/        # Router interface (FRR, Noop)
│   │   ├── store/          # bbolt local persistence
│   │   ├── watcher/        # K8s informer-based CRD watcher
│   │   ├── reconciler/     # Per-VIP reconciler
│   │   └── healthcheck/    # Health check engine
│   └── pkg/otel/           # OpenTelemetry setup
├── octavia-driver/         # OpenStack Octavia provider driver (Python)
├── deploy/                 # Deployment artifacts
├── docs/                   # Documentation
└── test/                   # Tests
```

## Requirements

- **Go**: 1.25+ (development)
- **Kubernetes**: 1.28+ (runtime)
- **VPP**: 24.10 (recommended, Agent runtime)
- **FRRouting**: 8.0+ (Agent runtime, optional)
- **controller-gen**: For CRD/deepcopy code generation
- **Docker**: 20.10+ (optional)

## Quickstart

### 1. Clone and build

```bash
git clone https://github.com/akam1o/arca-lb.git
cd arca-lb
make deps
make build
```

### 2. Install the CRD

```bash
make manifests
kubectl apply -f config/crd/bases/
```

### 3. Deploy the Operator

```bash
kubectl apply -f config/rbac/
kubectl apply -f config/manager/
```

### 4. Deploy the Agent (DaemonSet)

```bash
kubectl apply -f config/agent/
```

### 5. Create a VirtualIP

```bash
kubectl apply -f config/samples/virtualip_sample.yaml
kubectl get vip
```

## Makefile Targets

```bash
make help          # Show available targets
make deps          # Download dependencies
make build         # Build operator and agent binaries
make test          # Run tests
make lint          # Run linters
make manifests     # Generate CRD manifests (controller-gen)
make generate      # Generate deepcopy methods (controller-gen)
make proto         # Generate Protocol Buffers code (v1)
make docker        # Build Operator and Agent images
make clean         # Remove build artifacts
```

## Documentation

For detailed documentation, see the `docs/` directory:

### Operations
- [Installation Guide](docs/installation.md) - Installation steps and setup
- [Configuration Guide](docs/configuration.md) - How to configure Operator and Agent
- [API Reference](docs/api.md) - CRD API reference and REST API (v1)
- [OpenStack Octavia Integration](docs/octavia.md) - Octavia provider driver setup
- [Octavia Operations Guide](docs/octavia-operations.md) - Octavia status checks and route ERROR recovery
- [Troubleshooting](docs/troubleshooting.md) - Common issues and fixes
- [Backend Server Setup Guide](docs/backend-setup.md) - How to configure backend servers

### Developer Docs
- [Architecture](docs/architecture.md) - System architecture and design
- [Development Environment](docs/development.md) - Dev environment setup and workflow
- [Contribution Guide](docs/contributing.md) - How to contribute to the project

## Contributing

Contributions are welcome! See [docs/contributing.md](docs/contributing.md).

## Contact

For inquiries, open an issue on [GitHub Issues](https://github.com/akam1o/arca-lb/issues). For security reports, use [GitHub Security Advisories](https://github.com/akam1o/arca-lb/security/advisories).

## License

Apache License 2.0
