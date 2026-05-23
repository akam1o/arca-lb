SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

BIN_DIR := bin
AGENT_BIN := $(BIN_DIR)/arcalb-agent
OPERATOR_BIN := $(BIN_DIR)/arcalb-operator

GOCACHE_DIR := $(CURDIR)/.gocache
GOMODCACHE_DIR := $(CURDIR)/.gomodcache
GOTMP_DIR := $(CURDIR)/.gotmp
GO_ENV := GOCACHE=$(GOCACHE_DIR) GOMODCACHE=$(GOMODCACHE_DIR) GOTMPDIR=$(GOTMP_DIR)
GOLANGCI_LINT_VERSION ?= v2.6.0
TOOLS_BIN_DIR := $(CURDIR)/bin/tools
CONTROLLER_GEN_VERSION ?= v0.16.5

PROTO_SRC := api/proto
PROTO_OUT := pkg/grpc
PROTO_PATH := $(PATH):$(shell go env GOPATH)/bin

DOCKER_OPERATOR_FILE ?= deploy/docker/Dockerfile.operator
DOCKER_AGENT_FILE ?= deploy/docker/Dockerfile.agent
DOCKER_AGENT_IMAGE ?= arcalb-agent:latest
DOCKER_OPERATOR_IMAGE ?= arcalb-operator:latest

CONTROLLER_GEN ?= $(TOOLS_BIN_DIR)/controller-gen
CRD_OPTIONS ?= crd:generateEmbeddedObjectMeta=true

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
build: goenv ## Build operator and agent binaries
	@mkdir -p $(BIN_DIR)
	$(GO_ENV) go build -o $(OPERATOR_BIN) ./cmd/operator
	$(GO_ENV) go build -o $(AGENT_BIN) ./cmd/arcalb-agent

.PHONY: test
test: goenv ## Run unit tests with race detector and coverage
	$(GO_ENV) go test -v -race -coverprofile=coverage.out ./...

.PHONY: lint
lint: goenv ## Run golangci-lint
	$(GO_ENV) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run --timeout=10m

.PHONY: proto
proto: ## Generate gRPC code from protobuf
	@PATH="$(PROTO_PATH)" command -v protoc >/dev/null 2>&1 || { echo "error: protoc is required"; exit 1; }
	@PATH="$(PROTO_PATH)" command -v protoc-gen-go >/dev/null 2>&1 || { echo "error: protoc-gen-go is required"; exit 1; }
	@PATH="$(PROTO_PATH)" command -v protoc-gen-go-grpc >/dev/null 2>&1 || { echo "error: protoc-gen-go-grpc is required"; exit 1; }
	@mkdir -p $(PROTO_OUT)
	PATH="$(PROTO_PATH)" protoc -I $(PROTO_SRC) \
		--go_out=$(PROTO_OUT) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_OUT) --go-grpc_opt=paths=source_relative \
		$(PROTO_SRC)/*.proto
	@# Normalize tool version headers to avoid CI diffs across protoc/plugin versions.
	@perl -pi -e 's|^// - protoc-gen-go v.*$$|// - protoc-gen-go (version omitted)|' $(PROTO_OUT)/*.pb.go
	@perl -pi -e 's|^// - protoc-gen-go-grpc v.*$$|// - protoc-gen-go-grpc (version omitted)|' $(PROTO_OUT)/*_grpc.pb.go
	@perl -pi -e 's|^// - protoc[[:space:]]+v.*$$|// - protoc             (version omitted)|' $(PROTO_OUT)/*.pb.go
	@perl -pi -e 's|^//[[:space:]]+protoc-gen-go[[:space:]]+v.*$$|//\tprotoc-gen-go (version omitted)|' $(PROTO_OUT)/*.pb.go
	@perl -pi -e 's|^//[[:space:]]+protoc[[:space:]]+v.*$$|//\tprotoc        (version omitted)|' $(PROTO_OUT)/*.pb.go

.PHONY: docker
docker: ## Build operator and agent Docker images
	$(call ensure_tool,docker)
	@test -f $(DOCKER_OPERATOR_FILE) || { echo "error: missing $(DOCKER_OPERATOR_FILE)"; exit 1; }
	@test -f $(DOCKER_AGENT_FILE) || { echo "error: missing $(DOCKER_AGENT_FILE)"; exit 1; }
	docker build -f $(DOCKER_OPERATOR_FILE) -t $(DOCKER_OPERATOR_IMAGE) .
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

##@ CRD / Code Generation

.PHONY: manifests
manifests: ## Generate CRD manifests via controller-gen
	$(call ensure_tool,$(CONTROLLER_GEN))
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./api/..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: ## Generate deepcopy methods via controller-gen
	$(call ensure_tool,$(CONTROLLER_GEN))
	$(CONTROLLER_GEN) object:headerFile="" paths="./api/..."

.PHONY: install-controller-gen
install-controller-gen: goenv ## Install controller-gen tool
	@mkdir -p $(TOOLS_BIN_DIR)
	$(GO_ENV) GOBIN=$(TOOLS_BIN_DIR) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

.PHONY: verify-generate
verify-generate: install-controller-gen manifests generate ## Verify CRD and deepcopy generated files are current
	git diff --exit-code -- api/v1alpha1/zz_generated.deepcopy.go config/crd/bases
