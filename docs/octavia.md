# OpenStack Octavia Integration

This document explains how to integrate arca-lb with OpenStack Octavia as a provider driver.

## Overview

arca-lb provides an Octavia provider driver that translates OpenStack load balancer API operations into VirtualIP custom resource operations on a Kubernetes cluster. This enables OpenStack users to provision high-performance VPP-based L3DSR load balancers through the standard Octavia API.

### Architecture

```
OpenStack Tenant (API / Horizon / CLI)
         │
         ▼
┌─────────────────────────────┐
│   Octavia API               │
│   ├─ amphora driver         │
│   └─ arca driver  ◄────────│── octavia-arca-driver (Python)
└─────────┬───────────────────┘
          │ Kubernetes API (VirtualIP CRD)
          ▼
┌─────────────────────────────┐
│   Kubernetes API Server     │
│   VirtualIP CRD             │
└──────┬──────────────┬───────┘
       │              │
       ▼              ▼
   Operator        Agent (DaemonSet)
   (Status)        (VPP + FRR)
```

### Concept Mapping

| Octavia | arca-lb VirtualIP | Notes |
|---------|-------------------|-------|
| Loadbalancer | VIP address | One LB can have multiple VirtualIPs (one per listener) |
| Listener | VirtualIP resource | port + protocol define a VirtualIP |
| Pool | backends[] | Maps to the backend list |
| Member | backends[].address + monitorAddress + weight | Individual backend entry. `monitor_address` maps to `monitorAddress` for health checks. |
| HealthMonitor | healthCheck | HTTP, HTTPS, TCP, PING, and TLS-HELLO are supported. UDP-CONNECT is rejected. |
| L7Policy/Rule | *(not supported)* | arca-lb is an L4 load balancer |

Member weight is preserved in the VirtualIP backend spec, but it is not applied to data-plane traffic today. With the current VPP LB plugin path it is metadata only; weighted AS programming will take effect once the VPP LB API exposes backend weights.

## Prerequisites

- OpenStack with Octavia deployed (Zed or later recommended)
- Kubernetes cluster with arca-lb operator and agent running
- Network connectivity between Octavia API host and Kubernetes API server
- Python 3.9+

## Installation

### 1. Install the driver package

```bash
cd octavia-driver/
pip install .
```

Or install directly from the repository:

```bash
pip install git+https://github.com/akam1o/arca-lb.git#subdirectory=octavia-driver
```

### 2. Configure Octavia

Add the arca driver to `/etc/octavia/octavia.conf`:

```ini
[api_settings]
enabled_provider_drivers = amphora:The Octavia Amphora driver., arca:ArcaLB VPP-based L3DSR LB driver.
default_provider_driver = amphora

[driver_arca]
# Path to kubeconfig file. Leave empty for in-cluster config.
kubernetes_config = /etc/octavia/kubeconfig

# Kubernetes namespace where VirtualIP resources are created.
namespace = arca-system

# Default encapsulation type (GRE4, GRE6, L3DSR, NAT4, NAT6).
default_encap_type = L3DSR

# Default DSCP value for DSCP-based L3DSR mode (1-63).
default_dscp = 10

# Interval in seconds for syncing VirtualIP status back to Octavia.
status_sync_interval = 10
```

### 3. Set up Kubernetes access

Create a kubeconfig with appropriate RBAC permissions:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: octavia-arca-driver
  namespace: arca-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: octavia-arca-driver
  namespace: arca-system
rules:
  - apiGroups: ["arca.io"]
    resources: ["virtualips"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: octavia-arca-driver
  namespace: arca-system
subjects:
  - kind: ServiceAccount
    name: octavia-arca-driver
    namespace: arca-system
roleRef:
  kind: Role
  name: octavia-arca-driver
  apiGroup: rbac.authorization.k8s.io
```

```bash
kubectl apply -f octavia-rbac.yaml
```

### 4. Restart Octavia API

```bash
systemctl restart octavia-api
```

### 5. Verify the driver is loaded

```bash
openstack loadbalancer provider list
```

Expected output:

```
+---------+---------------------------------------------+
| name    | description                                 |
+---------+---------------------------------------------+
| amphora | The Octavia Amphora driver.                 |
| arca    | ArcaLB VPP-based L3DSR LB driver.           |
+---------+---------------------------------------------+
```

## Usage

### Create a load balancer

```bash
# Create a loadbalancer with the arca provider
openstack loadbalancer create \
  --name web-lb \
  --provider arca \
  --vip-address 203.0.113.10 \
  --vip-subnet-id <subnet-id>

# Create a listener
openstack loadbalancer listener create \
  --name web-listener \
  --protocol TCP \
  --protocol-port 80 \
  web-lb

# Create a pool
openstack loadbalancer pool create \
  --name web-pool \
  --protocol TCP \
  --lb-algorithm SOURCE_IP \
  --listener web-listener

# Add members
openstack loadbalancer member create \
  --name backend-1 \
  --address 10.0.1.1 \
  --protocol-port 80 \
  --weight 100 \
  web-pool

openstack loadbalancer member create \
  --name backend-2 \
  --address 10.0.1.2 \
  --protocol-port 80 \
  --weight 100 \
  web-pool

# Create a health monitor
openstack loadbalancer healthmonitor create \
  --name web-hm \
  --type HTTP \
  --delay 10 \
  --timeout 5 \
  --max-retries 3 \
  --max-retries-down 2 \
  --http-method GET \
  --url-path /healthz \
  --expected-codes 200 \
  web-pool
```

### Verify the VirtualIP was created

```bash
kubectl get virtualips -n arca-system
```

Expected output:

```
NAME                       ADDRESS        PORT   PROTOCOL   HEALTHY   TOTAL   AGE
octavia-xxxxxxxx-yyyyyyyy  203.0.113.10   80     TCP        2         2       30s
```

### Using flavors for encapsulation settings

Create an Octavia flavor to customize encapsulation type:

```bash
# Create a flavor profile
openstack loadbalancer flavorprofile create \
  --name arca-gre4 \
  --provider arca \
  --flavor-data '{"encap_type": "GRE4"}'

# Create a flavor
openstack loadbalancer flavor create \
  --name gre4-lb \
  --flavorprofile arca-gre4

# Use the flavor when creating a loadbalancer
openstack loadbalancer create \
  --name gre-lb \
  --provider arca \
  --flavor gre4-lb \
  --vip-address 203.0.113.20 \
  --vip-subnet-id <subnet-id>
```

Available flavor metadata:

| Key | Values | Default | Description |
|-----|--------|---------|-------------|
| `encap_type` | GRE4, GRE6, L3DSR, NAT4, NAT6 | L3DSR | Encapsulation type for return traffic |
| `dscp` | 1-63 | 10 | DSCP marking value for DSCP-based L3DSR |

## Limitations

- **L4 only**: L7 policies and rules are not supported. arca-lb is a Layer 4 load balancer.
- **Load balancing algorithm**: VPP uses Maglev consistent hashing internally. The `lb_algorithm` parameter is accepted but the underlying algorithm is always Maglev (functionally similar to `SOURCE_IP`).
- **Member weight**: Octavia member `weight` is stored in the VirtualIP backend spec, but it does not affect live traffic distribution yet. Support will be wired into the data plane once the VPP LB API exposes backend weights.
- **Backup members**: Octavia member `backup=True` is not supported. The driver rejects backup members instead of treating them as active backends.
- **Failover**: Manual failover is not supported. arca-lb relies on BGP ECMP for automatic failover across LB nodes.
- **Floating IP**: VIP addresses are managed by arca-lb's BGP announcements, not by Neutron floating IPs.
- **TERMINATED_HTTPS**: TLS termination is not supported at the LB level. Use TCP passthrough with backend-side TLS.
- **UDP-CONNECT health monitors**: Not supported. The driver rejects this type instead of silently mapping it to a TCP probe.

## Troubleshooting

### Driver not listed in providers

1. Verify the package is installed: `pip show octavia-arca-driver`
2. Check entry points: `pip show -f octavia-arca-driver | grep entry`
3. Verify `octavia.conf` has `arca` in `enabled_provider_drivers`
4. Check Octavia API logs: `journalctl -u octavia-api`

### VirtualIP not created

1. Check the Octavia API logs for errors from the arca driver
2. Verify Kubernetes connectivity: `kubectl --kubeconfig /etc/octavia/kubeconfig get virtualips -n arca-system`
3. Check RBAC permissions for the service account

### VirtualIP created but not programmed

1. Check the arca-lb agent logs: `kubectl logs -n arca-system -l app.kubernetes.io/name=arca-lb-agent`
2. Verify CRD status: `kubectl describe virtualip -n arca-system <name>`
3. Check VPP status: `vppctl show lb vip verbose`

### Status not syncing to Octavia

1. Verify `status_sync_interval` in `octavia.conf`
2. Check that the VirtualIP has the `Ready` condition set
3. Review the driver logs for status update errors
