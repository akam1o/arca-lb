# arca-lb Configuration Guide

This document explains how to configure arca-lb.

## Agent Configuration (v2)

The Agent reads its configuration from a YAML file. The path is specified via the `--config` flag or the `ARCA_AGENT_CONFIG` environment variable (default: `/etc/arca-lb/agent.yaml`).

The example below explicitly enables the production VPP data plane and FRR routing. When omitted, those backends default to `noop`.

```yaml
agent:
  id: "agent-01"
  storePath: "/var/lib/arca-lb/agent.db"
  reconcileInterval: "30s"

kubernetes:
  kubeconfig: ""           # empty = in-cluster config
  namespace: ""            # empty = watch all namespaces
  resyncInterval: "30s"

dataplane:
  type: "vpp"              # "vpp" or "noop"
  vpp:
    socket_path: "/run/vpp/api.sock"
    retained_vip_tuning_drift_policy: "rolling_recreate"
    retained_vip_tuning_drift_drain: "30s"

routing:
  enabled: true
  type: "frr"              # "frr" or "noop"
  vtyshPath: "/usr/bin/vtysh"
  routeTag: 10000
  cmdTimeout: "10s"

rollout:
  enabled: true
  leaseNamespace: "arca-lb-system"
  leaseDuration: "2m"
  retryInterval: "1s"

healthCheck:
  workerCount: 4
  maxConcurrentChecks: 64
  defaultTimeout: "3s"

metrics:
  enabled: true
  address: "127.0.0.1:9090"
  path: "/metrics"

telemetry:
  otlpEndpoint: ""         # empty = disabled
  otlpInsecure: false      # true only for plaintext collectors

log:
  level: "info"            # "debug", "info", "warn", "error"
  format: "json"           # "json" or "text"
```

### Agent settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `agent.id` | Unique identifier for the Agent | Hostname |
| `agent.storePath` | Path to the bbolt database file | `/var/lib/arca-lb/agent.db` |
| `agent.reconcileInterval` | Interval for periodic reconciliation | `30s` |

### Kubernetes settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `kubernetes.kubeconfig` | Path to kubeconfig file (empty = in-cluster) | `""` |
| `kubernetes.namespace` | Namespace to watch for VirtualIP resources (empty watches all namespaces) | `""` |
| `kubernetes.resyncInterval` | Informer resync interval | `30s` |

### DataPlane settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `dataplane.type` | Data plane backend (`vpp` or `noop`) | `noop` |
| `dataplane.vpp.socket_path` | VPP API socket path | `/run/vpp/api.sock` |
| `dataplane.vpp.retained_vip_tuning_drift_policy` | Handling for retained VIP tuning drift (`rolling_recreate` or `preserve`) | `rolling_recreate` |
| `dataplane.vpp.retained_vip_tuning_drift_drain` | Drain time before recreating a retained VIP with tuning drift | `30s` |

### Routing settings

When `routing.type` is `frr`, the Agent uses `vtysh` to manage static VIP
routes. In Kubernetes deployments, FRR is expected to run on the node and expose
its runtime socket directory at `/run/frr`; the Agent does not start FRR or
configure BGP peers.

The bundled `config/agent/daemonset.yaml` is the FRR-required production
manifest. If `routing.enabled: false` or `routing.type: noop` is needed for
development or data-plane-only validation, deploy
`config/agent-no-frr/` instead so the Pod does not wait on `vtysh` or mount
`/run/frr`.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `routing.enabled` | Enable BGP route management | `false` |
| `routing.type` | Router backend (`frr` or `noop`) | `noop` |
| `routing.vtyshPath` | Path to `vtysh` command | `/usr/bin/vtysh` |
| `routing.routeTag` | Tag value for static routes | `10000` |
| `routing.cmdTimeout` | Timeout for vtysh commands | `10s` |

### Rollout settings

When enabled, agents use Kubernetes `Lease` objects to serialize disruptive VIP
changes, such as VIP address changes and retained VIP rolling recreates, across
the cluster.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `rollout.enabled` | Enable cluster-wide rollout serialization | `false` |
| `rollout.leaseNamespace` | Namespace used for rollout Lease objects | Pod namespace, then `arca-lb-system` |
| `rollout.leaseDuration` | Lease duration before another agent may take over | `2m` |
| `rollout.retryInterval` | Wait interval while another agent holds the Lease | `1s` |

### HealthCheck settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `healthCheck.workerCount` | Number of health check worker goroutines | `4` |
| `healthCheck.maxConcurrentChecks` | Max concurrent checks per worker | `64` |
| `healthCheck.defaultTimeout` | Default probe timeout | `3s` |

### Metrics settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `metrics.enabled` | Enable Prometheus metrics endpoint | `false` |
| `metrics.address` | Listen address for the metrics server | `127.0.0.1:9090` |
| `metrics.path` | HTTP path for the metrics endpoint | `/metrics` |

The bundled Agent manifests bind metrics and health endpoints to loopback and
do not enable unauthenticated Prometheus auto-scrape annotations. Use a
kube-rbac-proxy, a Service with NetworkPolicy, or another authenticated scrape
path before exposing metrics outside the node-local loopback listener.

### Telemetry settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `telemetry.otlpEndpoint` | OTLP collector endpoint (empty = disabled) | `""` |
| `telemetry.otlpInsecure` | Disable TLS when exporting traces to a plaintext OTLP collector | `false` |

### Log settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `log.level` | Log level | `info` |
| `log.format` | Log format (`json` or `text`) | `json` |

## Operator Configuration

The Operator is configured via command-line flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--metrics-bind-address` | Address for the metrics endpoint | `127.0.0.1:8080` |
| `--health-probe-bind-address` | Address for the health probe endpoint | `:8081` |
| `--enable-webhooks` | Enable admission webhooks | `false` |
| `--leader-elect` | Enable leader election | `false` |

## Controller Secret Files

The controller can read sensitive values from files so Kubernetes Secrets can be
mounted without putting secret material directly in the YAML config. Do not set
the direct value and the matching file field at the same time.

| Parameter | Description |
|-----------|-------------|
| `server.api_key_file` | File containing the REST API key |
| `grpc.api_key_file` | File containing the gRPC API key |
| `datastore.mysql.password_file` | File containing the MySQL password |

## VirtualIP CRD Configuration

VIPs are configured as Kubernetes Custom Resources:

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: web-vip
  namespace: default
spec:
  address: 203.0.113.10
  port: 80
  protocol: TCP             # TCP or UDP
  encapType: L3DSR           # GRE4, GRE6, L3DSR, NAT4, NAT6
  dscp: 10                   # optional DSCP override, 1-63 (for DSCP-based L3DSR)
  backends:
    - address: 10.0.1.1
      weight: 1              # 1-100
    - address: 10.0.1.2
      weight: 1
  healthCheck:
    type: http               # http, https, tcp, ping, tls-hello
    intervalSeconds: 5
    timeoutSeconds: 3
    riseCount: 3
    fallCount: 2
    http:
      port: 8080
      path: /healthz
      method: GET
      expectedCodes: [200]
```

### VirtualIP Spec fields

| Field | Description | Required |
|-------|-------------|----------|
| `address` | Virtual IP address | Yes |
| `port` | Virtual port (1-65535) | Yes |
| `protocol` | Transport protocol (TCP, UDP) | Yes |
| `encapType` | Encapsulation type (GRE4, GRE6, L3DSR, NAT4, NAT6) | No (default: L3DSR) |
| `dscp` | Optional DSCP override for DSCP-based L3DSR (1-63 when set; omitted uses the agent default) | No |
| `backends` | List of backend servers | No |
| `healthCheck` | Health check configuration | No |

### Backend Spec fields

| Field | Description | Required |
|-------|-------------|----------|
| `address` | Backend IP address | Yes |
| `weight` | Desired traffic weight (1-100). Default: 1. Positive unequal weights are accepted and stored in the backend spec, but the current VPP LB plugin path treats them as metadata only; all listed backends are programmed without weight and live traffic distribution is not weighted. | No |

### HealthCheck Spec fields

| Field | Description | Required |
|-------|-------------|----------|
| `type` | Probe type (http, https, tcp, ping, tls-hello) | Yes |
| `intervalSeconds` | Probe interval in seconds | No (default: 5) |
| `timeoutSeconds` | Probe timeout in seconds | No (default: 3) |
| `riseCount` | Consecutive successes to mark healthy | No (default: 3) |
| `fallCount` | Consecutive failures to mark unhealthy | No (default: 2) |
| `http` | HTTP/HTTPS probe settings | No |
| `tcp` | TCP probe settings | No |

Health check timing fields are currently second-granularity fields. Millisecond-granularity intervals and timeouts are planned for a future API/model revision; use whole-second values in the current version.

## Environment Variables

### Agent (v2)

```bash
# Specify config file path
./bin/arcalb-agent --config /path/to/agent.yaml

# Or via environment variable
export ARCA_AGENT_CONFIG=/path/to/agent.yaml
./bin/arcalb-agent
```

## Next Steps

- See the [API Reference](./api.md) for the VirtualIP CRD schema
- See [Troubleshooting](./troubleshooting.md) for help resolving issues
