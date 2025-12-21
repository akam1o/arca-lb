# arca-lb Development Environment

This document explains how to set up the development environment for arca-lb.

## Requirements

### Required

- **Go**: 1.23+
- **Git**: 2.0+
- **Make**: 3.0+
- **Docker**: 20.10+ (optional, for integration tests)
- **Docker Compose**: 2.0+ (optional, for integration tests)

### Optional

- **golangci-lint**: Code quality checks
- **protoc**: Protocol Buffers compiler (for gRPC code generation)
- **etcd**: Datastore (for development)

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

#### golangci-lint

```bash
# macOS
brew install golangci-lint

# Linux
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.55.2
```

#### protoc

```bash
# macOS
brew install protobuf

# Linux (Ubuntu/Debian)
sudo apt-get install protobuf-compiler

# Install plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

### 4. Start local services

#### Launch etcd (Docker Compose)

```bash
cd deploy/docker-compose
docker compose -f docker-compose.dev.yml up -d etcd
```

#### Prepare config files

```bash
cp deploy/config/controller.example.yaml deploy/config/controller.yaml
cp deploy/config/agent.example.yaml deploy/config/agent.yaml
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

### 2. Update Protocol Buffers

```bash
# Edit proto files
# api/proto/*.proto

# Generate code
make proto
```

### 3. Build and test

```bash
# Build
make build

# Test
make test

# Coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 4. Run integration tests

```bash
# Run integration tests (requires etcd)
go test -tags=integration ./test/integration/...
```

## Debugging

### Debug the Controller

```bash
# Start with debug log level
./bin/arcalb-controller --config deploy/config/controller.yaml
# Or set log.level: "debug" in the config file
```

### Debug the Agent

```bash
# Start with debug log level
export ARCA_AGENT_CONFIG=deploy/config/agent.yaml
sudo ./bin/arcalb-agent
# Or set log.level: "debug" in the config file
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

## Code Style

### Go guidelines

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Format with `gofmt`
- Run `golangci-lint` for quality checks

### Naming

- **Packages**: lowercase, singular
- **Types**: PascalCase
- **Functions**: PascalCase (exported), camelCase (unexported)
- **Constants**: UPPER_SNAKE_CASE

### Error handling

```go
// Handle errors explicitly
if err != nil {
    return fmt.Errorf("context: %w", err)
}
```

### Logging

```go
// Use structured logging
logger.WithFields(logrus.Fields{
    "vip_id": vipID,
    "error": err,
}).Error("Failed to create VIP")
```

## Testing

### Unit tests

```bash
# Run all tests
make test

# Run a specific package
go test ./internal/controller/api/...

# Race detector
go test -race ./...
```

### Integration tests

```bash
# Run integration tests
go test -tags=integration ./test/integration/...

# Do not skip slow tests
go test -tags=integration -short=false ./test/integration/...
```

### Coverage

```bash
# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Release

### Version tags

```bash
# Create a version tag
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

### Build Docker images

```bash
# Build Docker images
make docker

# Build with a specific tag
docker build -f deploy/docker/Dockerfile.controller -t arcalb-controller:v1.0.0 .
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

### Linter errors

```bash
# Run linters
make lint

# Auto-fix applicable issues
golangci-lint run --fix
```

## Next Steps

- See [Architecture](./architecture.md) to understand the system design
- See the [Contribution Guide](./contributing.md) to contribute to the project
