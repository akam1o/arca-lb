SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

BIN_DIR := bin
CONTROLLER_BIN := $(BIN_DIR)/arcalb-controller
AGENT_BIN := $(BIN_DIR)/arcalb-agent

GOCACHE_DIR := $(CURDIR)/.gocache
GOMODCACHE_DIR := $(CURDIR)/.gomodcache
GOTMP_DIR := $(CURDIR)/.gotmp
GO_ENV := GOCACHE=$(GOCACHE_DIR) GOMODCACHE=$(GOMODCACHE_DIR) GOTMPDIR=$(GOTMP_DIR)

PROTO_SRC := api/proto
PROTO_OUT := pkg/grpc

DOCKER_CONTROLLER_FILE ?= deploy/docker/Dockerfile.controller
DOCKER_AGENT_FILE ?= deploy/docker/Dockerfile.agent
DOCKER_CONTROLLER_IMAGE ?= arcalb-controller:latest
DOCKER_AGENT_IMAGE ?= arcalb-agent:latest

define ensure_tool
	@command -v $(1) >/dev/null 2>&1 || { echo "error: $(1) is required"; exit 1; }
endef

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_\-]+:.*##/ {printf "\033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: goenv
goenv: ## Prepare local Go cache directories
	@mkdir -p $(GOCACHE_DIR) $(GOMODCACHE_DIR) $(GOTMP_DIR)

.PHONY: build
build: goenv ## Build controller and agent binaries
	@mkdir -p $(BIN_DIR)
	$(GO_ENV) go build -o $(CONTROLLER_BIN) ./cmd/arcalb-controller
	$(GO_ENV) go build -o $(AGENT_BIN) ./cmd/arcalb-agent

.PHONY: test
test: goenv ## Run unit tests with race detector and coverage
	$(GO_ENV) go test -v -race -coverprofile=coverage.out ./...

.PHONY: lint
lint: goenv ## Run golangci-lint
	$(call ensure_tool,golangci-lint)
	$(GO_ENV) golangci-lint run --timeout=5m

.PHONY: proto
proto: ## Generate gRPC code from protobuf
	$(call ensure_tool,protoc)
	$(call ensure_tool,protoc-gen-go)
	$(call ensure_tool,protoc-gen-go-grpc)
	@mkdir -p $(PROTO_OUT)
	protoc -I $(PROTO_SRC) \
		--go_out=$(PROTO_OUT) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_OUT) --go-grpc_opt=paths=source_relative \
		$(PROTO_SRC)/*.proto

.PHONY: docker
docker: ## Build controller and agent Docker images
	$(call ensure_tool,docker)
	@test -f $(DOCKER_CONTROLLER_FILE) || { echo "error: missing $(DOCKER_CONTROLLER_FILE)"; exit 1; }
	@test -f $(DOCKER_AGENT_FILE) || { echo "error: missing $(DOCKER_AGENT_FILE)"; exit 1; }
	docker build -f $(DOCKER_CONTROLLER_FILE) -t $(DOCKER_CONTROLLER_IMAGE) .
	docker build -f $(DOCKER_AGENT_FILE) -t $(DOCKER_AGENT_IMAGE) .

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) coverage.out

.PHONY: fmt
fmt: goenv ## Format code
	$(GO_ENV) go fmt ./...

.PHONY: vet
vet: goenv ## Run go vet
	$(GO_ENV) go vet ./...

.PHONY: deps
deps: goenv ## Download dependencies
	$(GO_ENV) go mod download
