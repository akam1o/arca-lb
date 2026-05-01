# arca-lb Installation Guide

This document explains how to install arca-lb.

## Prerequisites

### Operator

- **Kubernetes**: 1.28+ (cluster)
- **kubectl**: configured for the target cluster
- **controller-gen**: for CRD generation (development only)

### Agent

- **Kubernetes**: 1.28+ (cluster, for VirtualIP CRD watching)
- **VPP**: 24.10 (recommended, runtime)
- **FRRouting**: 8.0+ (node-local runtime, required for BGP advertisements)

### Build tools

- **Go**: 1.24+ (for building from source)
- **Docker**: 20.10+ (optional, for container images)

## Installation Methods

### Method 1: Kubernetes (recommended)

#### 1. Build binaries (or use pre-built images)

```bash
git clone https://github.com/akam1o/arca-lb.git
cd arca-lb
make build
```

#### 2. Generate and install the CRD

```bash
make manifests
kubectl apply -f config/crd/bases/
```

Verify the CRD is registered:

```bash
kubectl get crd virtualips.arca.io
```

#### 3. Deploy the Operator

```bash
kubectl apply -f config/rbac/
kubectl apply -f config/manager/
```

Verify the Operator is running:

```bash
kubectl get pods -l app.kubernetes.io/name=arca-lb-operator
```

#### 4. Deploy the Agent (DaemonSet)

Before applying the Agent DaemonSet, install and start FRR on every LB node. The
DaemonSet mounts the node's `/run/frr` directory and uses `vtysh` to add or
remove static VIP routes. Configure BGP peers and static-route redistribution in
the node-local FRR configuration.

```bash
kubectl apply -f config/agent/
```

Verify the Agent is running on each LB node:

```bash
kubectl get pods -l app.kubernetes.io/name=arca-lb-agent
```

#### 5. Create a VirtualIP

```bash
kubectl apply -f config/samples/virtualip_sample.yaml
```

Verify:

```bash
kubectl get vip
```

### Method 2: Build Docker images

#### 1. Build images

```bash
make docker
```

Or build individually:

```bash
docker build -f deploy/docker/Dockerfile.operator -t arcalb-operator:latest .
docker build -f deploy/docker/Dockerfile.agent -t arcalb-agent:latest .
```

#### 2. Push to your registry and update manifests

Edit `config/manager/` and `config/agent/` manifests to reference your image registry.

### Method 3: Run Agent outside Kubernetes

The Agent can connect to a K8s API server from outside the cluster using a kubeconfig file:

```bash
./bin/arcalb-agent --config /path/to/agent.yaml
```

With the agent config pointing to a kubeconfig:

```yaml
kubernetes:
  kubeconfig: "/path/to/kubeconfig"
  namespace: "default"
```

**Note**: The Agent requires `sudo` if VPP socket access requires elevated privileges.

## Initial Configuration

### Agent configuration

1. Copy the example config

```bash
cp deploy/config/agent.example.yaml deploy/config/agent.yaml
```

2. Edit the config

Update `deploy/config/agent.yaml` with your data plane (VPP socket path), routing (FRR settings), and Kubernetes connection settings. See the [Configuration Guide](./configuration.md) for full reference.

## Verification

### Check the Operator

```bash
kubectl logs -l app.kubernetes.io/name=arca-lb-operator --tail=20
```

### Check the Agent

```bash
kubectl logs -l app.kubernetes.io/name=arca-lb-agent --tail=20
```

If metrics are enabled:

```bash
# Port-forward to the agent metrics port
kubectl port-forward ds/arca-lb-agent 9090:9090
curl http://localhost:9090/metrics
```

### Create and verify a VirtualIP

```bash
# Create a VIP
kubectl apply -f - <<EOF
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: test-vip
spec:
  address: 203.0.113.100
  port: 80
  protocol: TCP
  encapType: L3DSR
  dscp: 10
  backends:
    - address: 10.0.1.1
      weight: 1
EOF

# Check status
kubectl get vip test-vip -o yaml

# Clean up
kubectl delete vip test-vip
```

## Next Steps

- See the [Configuration Guide](./configuration.md) for detailed settings
- See the [API Reference](./api.md) for the VirtualIP CRD schema
