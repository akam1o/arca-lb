# arca-lb Architecture

This document describes the architecture and design principles of arca-lb.

## Overview

arca-lb is a Kubernetes-native load balancer management system. Users define VIPs as `VirtualIP` Custom Resources, an Operator handles validation and status, and Agents on each LB node watch the CRDs via K8s Informers to program VPP and FRR.

## System Architecture

```
┌─────────────────────────────────────────┐
│           kubectl / GitOps              │
│   (apply VirtualIP CRD manifests)       │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│      Kubernetes API Server              │
│  - VirtualIP CRD (arca.io/v1alpha1)     │
│  - CRD admission validation             │
└──────┬──────────────────────┬───────────┘
       │                      │
       ▼                      ▼
┌──────────────────┐ ┌───────────────────────────┐
│     Operator     │ │   Agent (per LB node)     │
│ ┌──────────────┐ │ │ ┌───────────────────────┐ │
│ │ VirtualIP    │ │ │ │ K8s Watcher           │ │
│ │ Reconciler   │ │ │ │ (Informer + EventHandler)│
│ │ - Validate   │ │ │ └───────────┬───────────┘ │
│ │ - Status     │ │ │             │              │
│ │ - Finalizer  │ │ │             ▼              │
│ └──────────────┘ │ │ ┌───────────────────────┐ │
│ ┌──────────────┐ │ │ │ Per-VIP Reconciler    │ │
│ │ Optional     │ │ │ │ (goroutine per VIP)   │ │
│ │ Webhook      │ │ │ └──┬─────────┬──────┬──┘ │
│ └──────────────┘ │ │    │         │      │     │
└──────────────────┘ │    ▼         ▼      ▼     │
                     │ ┌──────┐ ┌──────┐ ┌─────┐ │
                     │ │ Data │ │Router│ │ HC  │ │
                     │ │Plane │ │(FRR) │ │Engine│ │
                     │ └──┬───┘ └──────┘ └─────┘ │
                     │    │                       │
                     │ ┌──┴───────────────┐       │
                     │ │ bbolt Local Store│       │
                     │ └──────────────────┘       │
                     │ ┌──────────────────┐       │
                     │ │ OTel + Prometheus│       │
                     │ └──────────────────┘       │
                     └────────────┬───────────────┘
                                  │
                                  ▼
                     ┌────────────────────────────┐
                     │ VPP (Vector Packet Processing) │
                     │ - L4 Load Balancer Plugin       │
                     │ - Maglev Hashing                │
                     └────────────────────────────┘
```

## Component Details

### Operator

The Operator runs as a Deployment in the Kubernetes cluster:

1. **VirtualIPReconciler**: Watches VirtualIP CRDs, updates `.status` fields (observedGeneration, healthyBackends, conditions), manages Finalizers
2. **CRD admission validation**: Validates VirtualIP resources at creation/update time (IP format, port range, protocol, DSCP, backend weights, health check config). The webhook implementation is optional for checks that cannot be expressed in the CRD schema.

### Agent

The Agent runs as a DaemonSet on each load balancer node:

1. **Watcher**: Uses K8s Informers to watch VirtualIP CRDs and emit Add/Update/Delete events
2. **Per-VIP Reconciler**: Spawns one goroutine per VIP; on each event, computes the desired state diff and syncs DataPlane + Router
3. **DataPlane (interface)**: Abstracts VPP control. Implementations: `VPPDataPlane` (production), `NoopDataPlane` (testing)
4. **Router (interface)**: Abstracts FRR/BGP route management. Implementations: `FRRRouter` (production), `NoopRouter` (testing)
5. **HealthCheck Engine**: Runs probes (HTTP/HTTPS, TCP, Ping, TLS hello) per VIP, fires callbacks on state transitions to trigger reconciliation
6. **bbolt Store**: Local embedded key-value store for caching VIP state and health check results; provides resilience against K8s API unavailability
7. **Metrics / Telemetry**: Prometheus endpoint + OpenTelemetry (OTLP) for traces and metrics

## Data Flows

### VIP creation flow

```
1. User → Kubernetes API
   kubectl apply -f virtualip.yaml

2. API Server → CRD schema validation
   Validate VirtualIP spec

3. API Server → etcd
   Store VirtualIP resource

4. Informer (Agent) → Watcher
   Receive Add event

5. Watcher → Per-VIP Reconciler
   OnVIPUpdate(vip)

6. Reconciler → DataPlane (VPP)
   EnsureVIP() + SyncBackends()

7. Reconciler → Router (FRR)
   AnnounceRoute()

8. Watcher → HealthCheck Engine
   UpdateVIP() → start probes
```

### Health check flow

```
1. HealthCheck Engine → Prober
   Probe() at configured interval

2. Prober → Backend Server
   HTTP/TCP/Ping request

3. Prober → HealthCheck Engine
   V2ProbeResult (success/failure)

4. HealthCheck Engine → State Tracker
   Update rise/fall counters

5. HealthCheck Engine → Reconciler (callback)
   OnHealthChange(vipName)

6. Reconciler → DataPlane (VPP)
   SyncBackends() (add/remove unhealthy backends)

7. Reconciler → Router (FRR)
   WithdrawRoute() if no healthy backends
```

### VIP deletion flow

```
1. User → Kubernetes API
   kubectl delete virtualip web-vip

2. Informer (Agent) → Watcher
   Receive Delete event

3. Watcher → HealthCheck Engine
   StopVIP()

4. Watcher → Per-VIP Reconciler
   OnVIPDelete(vip)

5. Reconciler → DataPlane (VPP)
   DeleteVIP()

6. Reconciler → Router (FRR)
   WithdrawRoute()
```

## Design Principles

### 1. Kubernetes-native

- VirtualIP CRD is the single source of truth.
- Users manage VIPs declaratively with `kubectl` or GitOps.
- No custom REST API or datastore required.

### 2. Declarative configuration

- Users specify the desired state in VirtualIP resources.
- Agents detect drift between current and desired states and reconcile.

### 3. Event-driven

- K8s Informers deliver changes to Agents in real time.
- Health check state changes trigger immediate reconciliation.

### 4. Resilience

- Agents continue running even if the K8s API is unavailable, using the local bbolt store.
- VPP configuration persists after Agent shutdown (graceful shutdown).

### 5. Pluggable interfaces

- DataPlane and Router are Go interfaces, enabling test doubles and alternative backends.
- Noop implementations simplify development and testing without VPP/FRR.

### 6. Observability

- OpenTelemetry for traces and metrics (OTLP export).
- Prometheus metrics endpoint for monitoring.
- Structured logs (`log/slog`) for debugging.

## Technology Stack

### Operator

- **Language**: Go 1.25 module language version, built and tested with Go toolchain 1.26.3
- **Framework**: controller-runtime (sigs.k8s.io/controller-runtime)
- **Validation**: CRD OpenAPI/CEL validation, with an optional admission webhook implementation

### Agent

- **Language**: Go 1.25 module language version, built and tested with Go toolchain 1.26.3
- **K8s integration**: client-go Informers
- **VPP integration**: go.fd.io/govpp v0.13.0
- **FRR integration**: via `vtysh`
- **Local store**: go.etcd.io/bbolt
- **Metrics**: Prometheus client_golang + OpenTelemetry

## Scalability

### Horizontal scaling

- Operator: single instance with leader election (or multiple replicas)
- Agent: one per LB node (deployed as a DaemonSet)

### Performance

- High-speed packet processing with VPP (user space)
- Per-VIP reconciler goroutines enable parallel processing
- Asynchronous, parallel health checks with worker pool
- Efficient reconciliation (updates only the diffs)

## Security

### Current implementation

- K8s RBAC restricts access to VirtualIP CRDs
- CRD OpenAPI/CEL validation rejects invalid VirtualIP specs at admission time
- Agent runs with least-privilege RBAC (read-only for VirtualIP resources)

### Recommendations

- Enable TLS for Agent ↔ K8s API communication
- Use NetworkPolicy to restrict Agent traffic
- Run VPP with appropriate security profiles

## Next Steps

- See [Development Environment](./development.md) to get started
- See the [Contribution Guide](./contributing.md) to contribute to the project
