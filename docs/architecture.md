# arca-lb Architecture

This document describes the architecture and design principles of arca-lb.

## Overview

arca-lb is a centralized load balancer management system. The Controller centrally manages VIPs and backends, and Agents control VPP on each node to perform load balancing.

## System Architecture

```
┌─────────────────────────────────────────┐
│             REST API Client             │
│      (kubectl, curl, management UI)     │
└────────────┬────────────────────────────┘
             │ HTTP/REST
             ▼
┌─────────────────────────────────────────┐
│               Controller                │
│  ┌──────────────────────────────────┐  │
│  │  REST API Server (Gin)            │  │
│  │  - VIP/Backend CRUD               │  │
│  │  - Config management              │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  gRPC Server                      │  │
│  │  - Config delivery to Agents      │  │
│  │  - Agent registration             │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  DataStore (etcd/MySQL)           │  │
│  │  - VIP/Backend persistence        │  │
│  │  - Revision management            │  │
│  └──────────────────────────────────┘  │
└────────────┬────────────────────────────┘
             │ gRPC
             ▼
┌─────────────────────────────────────────┐
│      Agent (per load balancer node)     │
│  ┌──────────────────────────────────┐  │
│  │  gRPC Client                      │  │
│  │  - Receives config from Controller │ │
│  │  - Sends heartbeats               │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  State Manager                   │  │
│  │  - Stores current config state   │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  Reconciler                      │  │
│  │  - Detects config drift          │  │
│  │  - Syncs to components           │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  VPP Syncer                      │  │
│  │  - Controls VPP LB plugin        │  │
│  │  - Applies VIP/Backend config    │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  Health Check Manager            │  │
│  │  - Backend health checks         │  │
│  │  - State management              │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  FRR Manager                     │  │
│  │  - Controls BGP announcements    │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  Metrics Server                  │  │
│  │  - Exposes Prometheus metrics    │  │
│  └──────────────────────────────────┘  │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│      VPP (Vector Packet Processing)      │
│  - L4 Load Balancer Plugin               │
│  - High-speed packet processing          │
└─────────────────────────────────────────┘
```

## Component Details

### Controller

The Controller is the central component and is responsible for:

1. **REST API Server**: Provides VIP and backend CRUD
2. **gRPC Server**: Delivers configs to Agents and manages Agent registration
3. **DataStore**: Persists VIPs and backends (etcd or MySQL)

### Agent

The Agent runs on each load balancer node and is responsible for:

1. **gRPC Client**: Receives configs from the Controller
2. **State Manager**: Stores current config state
3. **Reconciler**: Detects config drift and syncs components
4. **VPP Syncer**: Controls the VPP LB plugin
5. **Health Check Manager**: Executes backend health checks
6. **FRR Manager**: Controls BGP route announcements
7. **Metrics Server**: Exposes Prometheus metrics

## Data Flows

### VIP creation flow

```
1. REST API Client → Controller REST API
   POST /api/v1/vips

2. Controller → DataStore
   CreateVIP()

3. Controller → Agent (gRPC)
   ConfigSync.GetConfig() or WatchConfig()

4. Agent → State Manager
   UpdateConfig()

5. Agent → Reconciler
   TriggerReconcile()

6. Agent → VPP Syncer
   SyncVIP()

7. Agent → FRR Manager (optional)
   AnnounceRoute()

8. Agent → Health Check Manager
   StartHealthCheck()
```

### Health check flow

```
1. Health Check Manager → Prober
   Probe()

2. Prober → Backend Server
   HTTP/TCP/Ping request

3. Prober → Health Check Manager
   ProbeResult

4. Health Check Manager → State Tracker
   UpdateState()

5. Health Check Manager → VPP Syncer (if needed)
   UpdateBackendState()
```

## Design Principles

### 1. Centralized control

- The Controller is the single source of truth.
- Agents passively receive configs from the Controller.

### 2. Declarative configuration

- Users specify the desired state.
- Agents detect drift between current and desired states and reconcile.

### 3. Event-driven

- Config changes are delivered in real time via gRPC streams.
- Agents apply changes immediately.

### 4. Resilience

- Agents continue running even if the Controller connection drops.
- VPP configuration persists after Agent shutdown (graceful shutdown).

### 5. Observability

- Prometheus metrics for monitoring.
- Structured logs for debugging.

## Technology Stack

### Controller

- **Language**: Go 1.23
- **Web framework**: Gin
- **gRPC**: google.golang.org/grpc
- **Datastore**: etcd (recommended) or MySQL

### Agent

- **Language**: Go 1.23
- **VPP integration**: go.fd.io/govpp v0.13.0
- **FRR integration**: via `vtysh`
- **Metrics**: Prometheus `client_golang`

## Scalability

### Horizontal scaling

- Controller: run multiple instances sharing the datastore
- Agent: one per node (deploy as a DaemonSet)

### Performance

- High-speed packet processing with VPP (user space)
- Asynchronous, parallel health checks
- Efficient reconciliation (updates only the diffs)

## Security

### Current implementation

- Authentication/authorization not implemented (must be added for production)
- TLS is optional (supported for gRPC)

### Recommendations

- Encrypt Controller-Agent traffic with TLS
- Implement authentication/authorization for the REST API
- Restrict datastore access appropriately

## Next Steps

- See [Development Environment](./development.md) to get started
- See the [Contribution Guide](./contributing.md) to contribute to the project


