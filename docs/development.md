# arca-lb Development Environment

This document explains how to set up the development environment for arca-lb.

## Requirements

### Required

- **Go**: 1.24+
- **Git**: 2.0+
- **Make**: 3.0+
- **Kubernetes**: 1.28+ (for integration testing)
- **kubectl**: configured for dev cluster

### Optional

- **golangci-lint**: Code quality checks
- **controller-gen**: CRD and DeepCopy code generation
- **protoc**: Protocol Buffers compiler (for v1 gRPC code generation)
- **Docker**: 20.10+ (for container images)
- **kind**: Local K8s cluster for testing

## Setup Steps

### 1. Clone the repository

```bash
git clone https://github.com/akam1o/arca-lb.git
cd arca-lb
```

### 2. Install dependencies

```bash
make deps
```

### 3. Install developer tools

#### controller-gen (required for CRD development)

```bash
make install-controller-gen
# or
go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest
```

#### golangci-lint

```bash
# macOS
brew install golangci-lint

# Linux
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.55.2
```

#### protoc (v1 only)

```bash
# macOS
brew install protobuf

# Linux (Ubuntu/Debian)
sudo apt-get install protobuf-compiler

# Install plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

### 4. Set up a dev Kubernetes cluster (optional)

```bash
# Using kind
kind create cluster --name arca-lb-dev

# Install CRDs
make manifests
kubectl apply -f config/crd/bases/
```

## Development Workflow

### 1. Change code

```bash
# Create a branch
git checkout -b feature/my-feature

# Edit code
# ...

# Format code
make fmt

# Run linters
make lint

# Run tests
make test
```

### 2. Update CRD types

When modifying `api/v1alpha1/types.go`:

```bash
# Regenerate CRD manifests
make manifests

# Regenerate DeepCopy methods
make generate

# Re-apply CRDs to the dev cluster
kubectl apply -f config/crd/bases/
```

### 3. Update Protocol Buffers (v1 only)

```bash
# Edit proto files
# api/proto/*.proto

# Generate code
make proto
```

### 4. Build and test

```bash
# Build v2 only
make build

# Build operator and agent
make build

# Test
make test

# Coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 5. Run locally

```bash
# Run the Operator (connects to current kubeconfig context)
./bin/arcalb-operator --metrics-bind-address=:8080

# Run the Agent (with noop data plane for testing)
./bin/arcalb-agent --config deploy/config/agent.yaml
```

## Debugging

### Debug the Operator

```bash
# Run with verbose logging
./bin/arcalb-operator --metrics-bind-address=:8080
# controller-runtime uses zap logger in dev mode by default
```

### Debug the Agent

```bash
# Set log.level: "debug" in the agent config file
./bin/arcalb-agent --config deploy/config/agent.yaml
```

Use the `noop` data plane and router for development without VPP/FRR:

```yaml
dataplane:
  type: "noop"
routing:
  enabled: false
  type: "noop"
```

### Debug VPP

```bash
# Connect to VPP CLI
sudo vppctl

# Check VIPs
show lb vip

# Check backends
show lb as
```

### Inspect VirtualIP resources

```bash
# List all VIPs
kubectl get vip -o wide

# View detailed status
kubectl get vip web-vip -o yaml

# Watch for changes
kubectl get vip -w
```

## Code Style

### Go guidelines

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Format with `gofmt`
- Run `golangci-lint` for quality checks

### Naming

- **Packages**: lowercase, singular
- **Types**: PascalCase
- **Functions**: PascalCase (exported), camelCase (unexported)
- **Constants**: PascalCase for exported, camelCase for unexported

### Error handling

```go
// Handle errors explicitly
if err != nil {
    return fmt.Errorf("context: %w", err)
}
```

### Logging

```go
// Use log/slog for structured logging (v2)
slog.Info("VIP reconciled",
    "vip", vipName,
    "backends", len(backends),
)
```

## Testing

### Unit tests

```bash
# Run all tests
make test

# Run a specific package
go test ./internal/agent/reconciler/...

# Race detector
go test -race ./...
```

### Integration tests

```bash
# Run integration tests
go test -tags=integration ./test/integration/...
```

### Coverage

```bash
# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Key Makefile Targets

```bash
make help          # Show all targets
make build         # Build Operator + Agent
make test          # Run tests with race detector
make lint          # Run golangci-lint
make manifests     # Generate CRD manifests
make generate      # Generate DeepCopy methods
make fmt           # Format code
make vet           # Run go vet
make clean         # Remove build artifacts
```

## Release

### Version tags

```bash
git tag -a v2.0.0 -m "Release v2.0.0"
git push origin v2.0.0
```

### Build Docker images

```bash
make docker
```

## Troubleshooting

### Build errors

```bash
# Refresh dependencies
go mod tidy
go mod download
```

### Test errors

```bash
# Clear test cache
go clean -testcache
go test ./...
```

### CRD not updating

```bash
# Regenerate and reapply
make manifests
kubectl apply -f config/crd/bases/
```

## Next Steps

- See [Architecture](./architecture.md) to understand the system design
- See the [Contribution Guide](./contributing.md) to contribute to the project
