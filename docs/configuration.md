# arca-lb Configuration Guide

This document explains how to configure arca-lb.

## Controller Configuration

The Controller configuration file is YAML and uses the structure below:

```yaml
server:
  host: "0.0.0.0"
  port: 8080

grpc:
  host: "0.0.0.0"
  port: 50051

datastore:
  type: "etcd"  # "etcd" or "mysql"
  etcd:
    endpoints:
      - "http://localhost:2379"
    key_prefix: "/arca-lb"
  mysql:
    host: "localhost"
    port: 3306
    user: "arcalb"
    password: "password"
    database: "arcalb"

log:
  level: "info"   # "debug", "info", "warn", "error"
  format: "json"  # "json" or "text"
```

### Server settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `host` | Host for the REST API server | `0.0.0.0` |
| `port` | Port for the REST API server | `8080` |
| `read_timeout` | Request read timeout | `10s` |
| `write_timeout` | Response write timeout | `10s` |
| `read_header_timeout` | Header read timeout | `5s` |
| `idle_timeout` | Idle timeout | `60s` |
| `max_header_bytes` | Max header size (bytes) | `1048576` (1MB) |
| `allowed_origins` | CORS allowed origins | `["http://localhost:3000"]` |

### gRPC settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `host` | gRPC server host | `0.0.0.0` |
| `port` | gRPC server port | `50051` |
| `tls` | Enable TLS | `false` |

### DataStore settings

#### etcd settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `endpoints` | List of etcd endpoints | `["http://localhost:2379"]` |
| `key_prefix` | Key prefix | `"/arca-lb"` |
| `tls` | Enable TLS | `false` |
| `cert_file` | TLS certificate file | - |
| `key_file` | TLS private key file | - |
| `ca_file` | CA certificate file | - |
| `dial_timeout` | Connection timeout | `5s` |
| `request_timeout` | Request timeout | `5s` |

#### MySQL settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `host` | MySQL host | `localhost` |
| `port` | MySQL port | `3306` |
| `user` | MySQL username | - |
| `password` | MySQL password | - |
| `database` | MySQL database name | - |

### Log settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `level` | Log level | `info` |
| `format` | Log format | `json` |

## Agent Configuration

The Agent configuration file is YAML and uses the structure below:

```yaml
agent:
  id: "agent-01"
  metadata:
    region: "us-west-1"
  reconcile_interval: "30s"
  heartbeat_interval: "10s"

controller:
  address: "localhost:50051"
  timeout: "10s"
  max_retries: 5
  retry_backoff: "1s"
  max_retry_backoff: "30s"
  tls:
    enabled: false
    cert_file: "/etc/arca-lb/certs/agent.crt"
    key_file: "/etc/arca-lb/certs/agent.key"
    ca_file: "/etc/arca-lb/certs/ca.crt"
    insecure_skip_verify: false

vpp:
  socket_path: "/run/vpp/api.sock"
  connect_timeout: "5s"
  reconnect_interval: "5s"
  max_reconnect_attempts: 0
  lb:
    encap_type: "GRE4"
    dscp: 1 # DSCP (L3DSR) mode only; must be 1-63 when L3DSR is used
    type: "CLUSTERIP"
    new_flows_table_length: 1024
    fail_on_all_backends_down: false

frr:
  enabled: true
  vtysh: "/usr/bin/vtysh"
  config_file: "/etc/frr/frr.conf"

health_check:
  worker_count: 4
  default_timeout: "3s"
  max_concurrent_checks: 100

metrics:
  enabled: true
  listen_address: "0.0.0.0:9090"
  path: "/metrics"
  timeout: "10s"

log:
  level: "info"
  format: "json"
  output: "stdout"
```

### Agent settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `agent.id` | Unique identifier for the Agent | Hostname |
| `agent.metadata` | Metadata for the Agent (key/value map) | - |
| `agent.reconcile_interval` | Interval to check for config drift | `30s` |
| `agent.heartbeat_interval` | Heartbeat interval | `10s` |

### Controller settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `controller.address` | Controller gRPC endpoint | `localhost:50051` |
| `controller.timeout` | Timeout for gRPC calls | `10s` |
| `controller.max_retries` | Max connection retry count | `5` |
| `controller.retry_backoff` | Initial retry backoff | `1s` |
| `controller.max_retry_backoff` | Max retry backoff | `30s` |
| `controller.tls.enabled` | Enable TLS | `false` |
| `controller.tls.cert_file` | Client certificate file | - |
| `controller.tls.key_file` | Client private key file | - |
| `controller.tls.ca_file` | CA certificate file | - |
| `controller.tls.insecure_skip_verify` | Skip server certificate verification | `false` |

### VPP settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `socket_path` | VPP API socket path | `/run/vpp/api.sock` |
| `connect_timeout` | Connection timeout | `5s` |
| `reconnect_interval` | Reconnect interval | `5s` |
| `max_reconnect_attempts` | Max reconnect attempts (0 = unlimited) | `0` |
| `lb.encap_type` | Encapsulation type (GRE4, GRE6, L3DSR, NAT4, NAT6) | `GRE4` |
| `lb.dscp` | DSCP value for DSCP (L3DSR) mode (0-63; must be 1-63 when L3DSR is used; not used for GRE/NAT) | `0` |
| `lb.type` | Load balancer service type (CLUSTERIP, NODEPORT) | `CLUSTERIP` |
| `lb.new_flows_table_length` | Flow table size for new connections | `1024` |
| `lb.fail_on_all_backends_down` | Fail VIP creation when all backends are down | `false` |

### FRR settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `enabled` | Enable FRR integration | `false` |
| `vtysh` | Path to `vtysh` command | `/usr/bin/vtysh` |
| `config_file` | FRR config file path | `/etc/frr/frr.conf` |

### Health Check settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `worker_count` | Concurrent health check workers | `4` |
| `default_timeout` | Default health check timeout | `3s` |
| `max_concurrent_checks` | Max concurrent checks per worker | `100` |

### Metrics settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `enabled` | Enable Prometheus metrics | `false` |
| `listen_address` | Listen address for the metrics server | `0.0.0.0:9090` |
| `path` | HTTP path for metrics endpoint | `/metrics` |
| `timeout` | Timeout for metrics HTTP server operations | `10s` |

### Log settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `level` | Log level | `info` |
| `format` | Log format | `json` |
| `output` | Log destination (`stdout`, `stderr`, or file path) | `stdout` |

## Environment Variables

### Controller

Specify the config file path with the `--config` flag:

```bash
./bin/arcalb-controller --config deploy/config/controller.yaml
```

### Agent

Specify the config file path via the `ARCA_AGENT_CONFIG` environment variable (default: `/etc/arca-lb/agent.yaml`):

```bash
export ARCA_AGENT_CONFIG=/path/to/agent.yaml
./bin/arcalb-agent
```

You can override settings with these environment variables:

- `ARCA_AGENT_ID` - Agent ID
- `ARCA_CONTROLLER_ADDRESS` - Controller gRPC endpoint
- `ARCA_VPP_SOCKET` - VPP socket path
- `ARCA_LOG_LEVEL` - Log level
- `ARCA_LOG_FORMAT` - Log format
- `ARCA_TLS_ENABLED` - Enable TLS (true/false)
- `ARCA_TLS_CERT` - TLS certificate file
- `ARCA_TLS_KEY` - TLS private key file
- `ARCA_TLS_CA` - TLS CA certificate file

## Validating the Configuration

Syntax errors are detected at startup. To validate, start the services:

```bash
# Controller
./bin/arcalb-controller --config deploy/config/controller.yaml

# Agent
ARCA_AGENT_CONFIG=/path/to/agent.yaml ./bin/arcalb-agent
```

## Next Steps

- See the [REST API Reference](./api.md) for API usage
- See [Troubleshooting](./troubleshooting.md) for help resolving issues
