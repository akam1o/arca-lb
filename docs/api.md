# arca-lb REST API Reference

This document describes the arca-lb Controller REST API.

## Base URL

```
http://localhost:8080
```

## Authentication

Authentication is not implemented in the current version. For production, add proper authentication and authorization.

## Endpoints

### Health checks

#### GET /healthz

Health check endpoint to verify the server is running.

**Response**

```json
{
  "status": "healthy",
  "time": "2025-12-20T10:00:00Z"
}
```

#### GET /readyz

Readiness endpoint to confirm the server is ready to serve requests.

**Responses**

- **200 OK**: Server is ready
  ```json
  {
    "status": "ready",
    "time": "2025-12-20T10:00:00Z"
  }
  ```

- **503 Service Unavailable**: Server not ready (e.g., datastore connection error)
  ```json
  {
    "status": "not ready",
    "error": "datastore unavailable"
  }
  ```

### VIP management

#### POST /api/v1/vips

Create a new VIP.

**Request body**

```json
{
  "vip": "192.168.1.100",
  "port": 80,
  "protocol": "TCP",
  "lb_method": "maglev",
  "encap_type": "L3DSR",
  "dscp": 10,
  "health_check": {
    "type": "http",
    "interval": "10s",
    "timeout": "5s"
  }
}
```

**Notes**
- `health_check.type` must be lowercase (`http`, `https`, `tcp`, `ping`)
- `health_check.interval` and `health_check.timeout` are Go duration strings (e.g. `10s`, `1m`)
- `rise_count` / `fall_count` are currently not configurable via REST API (defaults: 3/3)
- Do not set `health_check.vip_id` when creating a VIP (it is set automatically after creation)
- `encap_type` is optional (`GRE4`, `GRE6`, `L3DSR`, `NAT4`, `NAT6`); if omitted, the Agent default (`vpp.lb.encap_type`) is used
- `dscp` is optional (1-63) and is used only when `encap_type` resolves to `L3DSR` (DSCP mode); it is not used for GRE/NAT; if omitted, the Agent default (`vpp.lb.dscp`) is used; `dscp=0` is rejected in `L3DSR` mode

**Responses**

- **201 Created**: VIP created
- **400 Bad Request**: Invalid request
- **409 Conflict**: VIP already exists

#### GET /api/v1/vips

List all VIPs.

**Response**

```json
{
  "vips": [
    {
      "id": "vip-1",
      "vip": "192.168.1.100",
      "port": 80,
      "protocol": "TCP",
      "lb_method": "maglev",
      "encap_type": "L3DSR",
      "dscp": 10,
      "created_at": "2025-12-20T10:00:00Z",
      "updated_at": "2025-12-20T10:00:00Z"
    }
  ],
  "count": 1
}
```

#### GET /api/v1/vips/:id

Get a VIP by ID.

**Path parameter**

- `id`: VIP ID

**Responses**

- **200 OK**: VIP found
- **404 Not Found**: VIP not found

#### PUT /api/v1/vips/:id

Update a VIP by ID.

**Path parameter**

- `id`: VIP ID

**Request body**

```json
{
  "vip": "192.168.1.101",
  "port": 443,
  "protocol": "TCP",
  "lb_method": "maglev",
  "encap_type": "GRE4",
  "dscp": 0
}
```

**Responses**

- **200 OK**: VIP updated
- **400 Bad Request**: Invalid request
- **404 Not Found**: VIP not found

#### DELETE /api/v1/vips/:id

Delete a VIP by ID.

**Path parameter**

- `id`: VIP ID

**Responses**

- **200 OK**: VIP deleted
- **404 Not Found**: VIP not found

### Backend management

#### POST /api/v1/backends

Add a backend.

**Request body**

```json
{
  "vip_id": "vip-1",
  "ip": "10.0.0.1",
  "weight": 10
}
```

**Responses**

- **201 Created**: Backend created
- **400 Bad Request**: Invalid request
- **404 Not Found**: VIP not found

#### GET /api/v1/backends?vip_id=:vip_id

List backends for a VIP.

**Query parameter**

- `vip_id` (required): VIP ID

**Response**

```json
{
  "backends": [
    {
      "id": "backend-1",
      "vip_id": "vip-1",
      "ip": "10.0.0.1",
      "weight": 10
    }
  ],
  "count": 1
}
```

#### GET /api/v1/backends/:id

Get a backend by ID.

**Path parameter**

- `id`: Backend ID

**Responses**

- **200 OK**: Backend found
- **404 Not Found**: Backend not found

#### PUT /api/v1/backends/:id

Update a backend by ID.

**Path parameter**

- `id`: Backend ID

**Request body**

```json
{
  "ip": "10.0.0.2",
  "weight": 20
}
```

**Responses**

- **200 OK**: Backend updated
- **400 Bad Request**: Invalid request
- **404 Not Found**: Backend not found

#### DELETE /api/v1/backends/:id

Delete a backend by ID.

**Path parameter**

- `id`: Backend ID

**Responses**

- **200 OK**: Backend deleted
- **404 Not Found**: Backend not found

### Revision management

#### GET /api/v1/revision

Get the current config revision.

**Response**

```json
{
  "revision": 123
}
```

## Error Responses

On errors, responses follow this format:

```json
{
  "error": "error message"
}
```

### HTTP status codes

- **200 OK**: Request succeeded
- **201 Created**: Resource created
- **400 Bad Request**: Invalid request
- **404 Not Found**: Resource not found
- **409 Conflict**: Resource already exists
- **500 Internal Server Error**: Server error

## Data Models

### VIP

```json
{
  "id": "string",
  "vip": "string (IP address)",
  "port": "integer (1-65535)",
  "protocol": "TCP | UDP",
  "lb_method": "maglev",
  "health_check": "HealthCheck (optional)",
  "created_at": "string (RFC3339)",
  "updated_at": "string (RFC3339)"
}
```

### Backend

```json
{
  "id": "string",
  "vip_id": "string",
  "ip": "string (IP address)",
  "weight": "integer (1-100)"
}
```

### HealthCheck

```json
{
  "id": "string",
  "vip_id": "string (omit when creating a VIP; present when retrieving)",
  "type": "http | https | tcp | ping (required)",
  "interval_sec": "integer (required, min=1)",
  "timeout_sec": "integer (required, min=1)",
  "rise_count": "integer (required, min=1)",
  "fall_count": "integer (required, min=1)",
  "config": {
    "port": "integer (HTTP/HTTPS/TCP only)",
    "path": "string (HTTP/HTTPS only)",
    "method": "string (HTTP/HTTPS only)",
    "expected_codes": "array of integers (HTTP/HTTPS only)"
  },
  "created_at": "string (RFC3339)",
  "updated_at": "string (RFC3339)"
}
```

**Notes**
- `type` must be lowercase (`http`, `https`, `tcp`, `ping`)
- `vip_id` is read-only. Do not set it when creating a VIP; it is populated after creation and present when retrieving
- When creating a VIP via REST, use `health_check.interval` / `health_check.timeout` (duration strings). The stored model uses `interval_sec` / `timeout_sec`.

## Examples

### Create a VIP with cURL

```bash
curl -X POST http://localhost:8080/api/v1/vips \
  -H "Content-Type: application/json" \
  -d '{
    "vip": "192.168.1.100",
    "port": 80,
    "protocol": "TCP",
    "lb_method": "maglev"
  }'
```

### Add a backend

```bash
curl -X POST http://localhost:8080/api/v1/backends \
  -H "Content-Type: application/json" \
  -d '{
    "vip_id": "vip-1",
    "ip": "10.0.0.1",
    "weight": 10
  }'
```

### List VIPs

```bash
curl http://localhost:8080/api/v1/vips
```

## Next Steps

- See the [Configuration Guide](./configuration.md) for detailed settings
- See [Troubleshooting](./troubleshooting.md) to resolve issues
