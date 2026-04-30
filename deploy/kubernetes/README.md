# Kubernetes Manifests

The current arca-lb deployment is the v2 CRD/operator layout under `config/`:

- `config/manager/manager.yaml` for the operator Deployment
- `config/agent/daemonset.yaml` for the agent DaemonSet
- `config/rbac/` for RBAC
- `config/crd/bases/` for generated CRDs

This directory only contains optional add-on manifests.
The removed v1 controller/gRPC manifests are intentionally not mirrored here.
Do not deploy an `arcalb-controller` Service or port 50051 stack for v2.
