# arca-lb API Reference

This document is the API reference for arca-lb.

## VirtualIP CRD API (v2)

### Resource Overview

| Field | Value |
|-------|-------|
| API Group | `arca.io` |
| API Version | `v1alpha1` |
| Kind | `VirtualIP` |
| Short Name | `vip` |
| Scope | Namespaced |

### Terminology

`VirtualIP` is the canonical Kubernetes resource name. In this API,
`spec.address` is the virtual IP address, while backend `address` fields are
real server addresses. The `vip` kubectl short name, REST/legacy surfaces, and
Octavia `vip_address` all refer to the same virtual IP address concept.
`monitorAddress` is separate and is used only as the backend health-check
target.

### kubectl Examples

```bash
# List VIPs
kubectl get vip
kubectl get vip -o wide

# Create a VIP
kubectl apply -f virtualip.yaml

# View VIP status
kubectl get vip web-vip -o yaml

# Delete a VIP
kubectl delete vip web-vip

# Watch for changes
kubectl get vip -w
```

### VirtualIP Resource

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: web-vip
  namespace: default
spec:
  address: "203.0.113.10"
  port: 80
  protocol: TCP
  encapType: L3DSR
  dscp: 10
  backends:
    - address: "10.0.1.1"
      weight: 1
    - address: "10.0.1.2"
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
      method: GET
      expectedCodes: [200]
status:
  observedGeneration: 5
  healthyBackends: 2
  totalBackends: 2
  backends:
    - address: "10.0.1.1"
      healthy: true
      lastProbeTime: "2025-01-15T10:00:00Z"
    - address: "10.0.1.2"
      healthy: true
      lastProbeTime: "2025-01-15T10:00:00Z"
  conditions:
    - type: Ready
      status: "True"
      lastTransitionTime: "2025-01-15T09:00:00Z"
```

### Spec Fields

#### VirtualIPSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `address` | string (IP) | Yes | Virtual IP address |
| `port` | int (1-65535) | Yes | Virtual port number |
| `protocol` | string | Yes | Transport protocol: `TCP` or `UDP` |
| `encapType` | string | No | Encapsulation type: `GRE4`, `GRE6`, `L3DSR`, `NAT4`, `NAT6`. Default: `L3DSR` |
| `dscp` | int (1-63) | No | Optional DSCP override for DSCP-based L3DSR; omitted uses the agent default |
| `backends` | []BackendSpec | No | List of backend servers |
| `healthCheck` | HealthCheckSpec | No | Health check configuration |

#### BackendSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `address` | string (IP) | Yes | Backend server IP address |
| `monitorAddress` | string (IP) | No | Alternate backend IP address used only for health checks. Defaults to `address`. |
| `weight` | int (1-100) | No | Desired traffic weight. Default: 1. Positive unequal weights are accepted and stored in the backend spec, but the current VPP LB plugin path treats them as metadata only; all listed backends are programmed without weight and live traffic distribution is not weighted. |

#### HealthCheckSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | Probe type: `http`, `https`, `tcp`, `ping`, `tls-hello` |
| `intervalSeconds` | int (≥1) | No | Time between probes. Default: 5 |
| `timeoutSeconds` | int (≥1) | No | Max time to wait for response. Default: 3 |
| `riseCount` | int (≥1) | No | Consecutive successes to mark healthy. Default: 3 |
| `fallCount` | int (≥1) | No | Consecutive failures to mark unhealthy. Default: 2 |
| `http` | HTTPHealthCheck | No | HTTP/HTTPS-specific settings |
| `tcp` | TCPHealthCheck | No | TCP/TLS-HELLO-specific settings |

Note: Health check timing is currently stored and validated as whole seconds. Sub-second intervals and timeouts are not accepted in the current API/model. Millisecond-granularity health check timing is planned for a future API/model revision; use second-aligned values until then.

#### HTTPHealthCheck

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `port` | int (1-65535) | Yes | Target port |
| `path` | string | No | HTTP path. Default: `/` |
| `method` | string | No | HTTP method: `GET`, `HEAD`, `POST`. Default: `GET` |
| `host` | string | No | Host header override |
| `headers` | map[string]string | No | Additional HTTP headers |
| `expectedCodes` | []int | No | HTTP status codes indicating success |
| `skipTLSVerify` | bool | No | Skip TLS certificate verification (HTTPS only) |

#### TCPHealthCheck

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `port` | int (1-65535) | Yes | Target port for TCP or TLS-HELLO connection |
| `send` | string | No | Data to send after TCP connection; ignored for TLS-HELLO |
| `expectedResponse` | string | No | Expected substring in TCP response; ignored for TLS-HELLO |

### Status Fields

#### VirtualIPStatus

| Field | Type | Description |
|-------|------|-------------|
| `observedGeneration` | int64 | Most recent generation observed by the operator |
| `healthyBackends` | int | Number of healthy backends |
| `totalBackends` | int | Total number of configured backends |
| `backends` | []BackendStatus | Per-backend health status |
| `conditions` | []Condition | Standard Kubernetes conditions |

#### BackendStatus

| Field | Type | Description |
|-------|------|-------------|
| `address` | string | Backend IP address |
| `healthy` | bool | Whether the backend is healthy |
| `lastProbeTime` | Time | Timestamp of the most recent probe |
| `message` | string | Human-readable details |

### Print Columns

When using `kubectl get vip`:

| Column | Source |
|--------|--------|
| NAME | `metadata.name` |
| Address | `spec.address` |
| Port | `spec.port` |
| Protocol | `spec.protocol` |
| Healthy | `status.healthyBackends` |
| Total | `status.totalBackends` |
| Age | `metadata.creationTimestamp` |

### Validation Rules

The CRD schema and optional admission webhook enforce:

- `address` must be a valid IP address
- `port` must be between 1 and 65535
- `protocol` must be `TCP` or `UDP`
- `encapType` must be one of: `GRE4`, `GRE6`, `L3DSR`, `NAT4`, `NAT6`
- `dscp`, when set, must be between 1 and 63
- Backend `weight` must be between 1 and 100
- Backend `address` must be a valid IP address
- HealthCheck `type` must be one of: `http`, `https`, `tcp`, `ping`, `tls-hello`
- HealthCheck `http` is required when `type` is `http` or `https`
- HealthCheck `tcp` is required when `type` is `tcp` or `tls-hello`
- HealthCheck probe ports must be between 1 and 65535
- HealthCheck `timeoutSeconds` must be less than `intervalSeconds`

### Examples

#### L3DSR with HTTP health check

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
    - address: 10.0.1.3
      weight: 50
  healthCheck:
    type: http
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

#### NAT4 with TCP health check

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: db-vip
spec:
  address: 203.0.113.20
  port: 3306
  protocol: TCP
  encapType: NAT4
  backends:
    - address: 10.0.2.1
      weight: 1
    - address: 10.0.2.2
      weight: 1
  healthCheck:
    type: tcp
    intervalSeconds: 10
    timeoutSeconds: 5
    riseCount: 2
    fallCount: 3
    tcp:
      port: 3306
```

#### TLS hello health check

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: tls-vip
spec:
  address: 203.0.113.25
  port: 443
  protocol: TCP
  encapType: NAT4
  backends:
    - address: 10.0.2.10
      weight: 1
  healthCheck:
    type: tls-hello
    intervalSeconds: 10
    timeoutSeconds: 5
    riseCount: 2
    fallCount: 3
    tcp:
      port: 443
```

#### GRE4 with Ping health check

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: dns-vip
spec:
  address: 203.0.113.30
  port: 53
  protocol: UDP
  encapType: GRE4
  backends:
    - address: 10.0.3.1
    - address: 10.0.3.2
  healthCheck:
    type: ping
    intervalSeconds: 3
    timeoutSeconds: 2
    riseCount: 2
    fallCount: 2
```

#### Minimal (no health check)

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: simple-vip
spec:
  address: 203.0.113.40
  port: 443
  protocol: TCP
  backends:
    - address: 10.0.4.1
    - address: 10.0.4.2
```
