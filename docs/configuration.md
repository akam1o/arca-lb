# arca-lb Configuration Guide

This document explains how to configure arca-lb.

## Agent Configuration (v2)

The Agent reads its configuration from a YAML file. The path is specified via the `--config` flag or the `ARCA_AGENT_CONFIG` environment variable (default: `/etc/arca-lb/agent.yaml`).

```yaml
agent:
  id: "agent-01"
  storePath: "/var/lib/arca-lb/agent.db"
  reconcileInterval: "30s"

kubernetes:
  kubeconfig: ""           # empty = in-cluster config
  namespace: "default"
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
  routeTag: 100
  cmdTimeout: "5s"

healthCheck:
  workerCount: 4
  maxConcurrentChecks: 100
  defaultTimeout: "3s"

metrics:
  enabled: true
  address: "0.0.0.0:9090"
  path: "/metrics"

telemetry:
  otlpEndpoint: ""         # empty = disabled

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
| `kubernetes.namespace` | Namespace to watch for VirtualIP resources | `default` |
| `kubernetes.resyncInterval` | Informer resync interval | `30s` |

### DataPlane settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `dataplane.type` | Data plane backend (`vpp` or `noop`) | `vpp` |
| `dataplane.vpp.socket_path` | VPP API socket path | `/run/vpp/api.sock` |
| `dataplane.vpp.retained_vip_tuning_drift_policy` | Handling for retained VIP tuning drift (`rolling_recreate` or `preserve`) | `rolling_recreate` |
| `dataplane.vpp.retained_vip_tuning_drift_drain` | Drain time before recreating a retained VIP with tuning drift | `30s` |

### Routing settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `routing.enabled` | Enable BGP route management | `false` |
| `routing.type` | Router backend (`frr` or `noop`) | `frr` |
| `routing.vtyshPath` | Path to `vtysh` command | `/usr/bin/vtysh` |
| `routing.routeTag` | Tag value for static routes | `100` |
| `routing.cmdTimeout` | Timeout for vtysh commands | `5s` |

### HealthCheck settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `healthCheck.workerCount` | Number of health check worker goroutines | `4` |
| `healthCheck.maxConcurrentChecks` | Max concurrent checks per worker | `100` |
| `healthCheck.defaultTimeout` | Default probe timeout | `3s` |

### Metrics settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `metrics.enabled` | Enable Prometheus metrics endpoint | `false` |
| `metrics.address` | Listen address for the metrics server | `0.0.0.0:9090` |
| `metrics.path` | HTTP path for the metrics endpoint | `/metrics` |

### Telemetry settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `telemetry.otlpEndpoint` | OTLP collector endpoint (empty = disabled) | `""` |

### Log settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `log.level` | Log level | `info` |
| `log.format` | Log format (`json` or `text`) | `json` |

## Operator Configuration

The Operator is configured via command-line flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--metrics-bind-address` | Address for the metrics endpoint | `:8080` |
| `--health-probe-bind-address` | Address for the health probe endpoint | `:8081` |
| `--enable-webhooks` | Enable admission webhooks | `false` |
| `--leader-elect` | Enable leader election | `false` |

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
      weight: 100            # 1-100
    - address: 10.0.1.2
      weight: 100
  healthCheck:
    type: http               # http, https, tcp, ping
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
| `weight` | Desired traffic weight (1-100). The VPP LB plugin path currently stores this as metadata; weighted AS programming will take effect once the VPP LB API exposes backend weights. | No (default: 100) |

### HealthCheck Spec fields

| Field | Description | Required |
|-------|-------------|----------|
| `type` | Probe type (http, https, tcp, ping) | Yes |
| `intervalSeconds` | Probe interval in seconds | No (default: 5) |
| `timeoutSeconds` | Probe timeout in seconds | No (default: 3) |
| `riseCount` | Consecutive successes to mark healthy | No (default: 3) |
| `fallCount` | Consecutive failures to mark unhealthy | No (default: 2) |
| `http` | HTTP/HTTPS probe settings | No |
| `tcp` | TCP probe settings | No |

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
