# octavia-arca-driver

Octavia provider driver for [ArcaLB](https://github.com/akam1o/arca-lb) — a high-performance VPP-based L3DSR load balancer.

This driver enables OpenStack users to provision arca-lb load balancers through the standard Octavia API by translating Octavia operations into VirtualIP custom resources on a Kubernetes cluster.

## Quick Start

```bash
pip install .
```

Add to `/etc/octavia/octavia.conf`:

```ini
[api_settings]
enabled_provider_drivers = amphora:The Octavia Amphora driver., arca:ArcaLB VPP-based L3DSR LB driver.

[driver_arca]
kubernetes_config = /etc/octavia/kubeconfig
namespace = arca-system
```

Restart Octavia API and verify:

```bash
openstack loadbalancer provider list
```

See [docs/octavia.md](../docs/octavia.md) for setup documentation and
[docs/octavia-operations.md](../docs/octavia-operations.md) for operational
checks and route ERROR recovery.

## Concept Mapping

| Octavia | arca-lb VirtualIP |
|---------|-------------------|
| Loadbalancer | VIP address |
| Listener | VirtualIP (port + protocol) |
| Pool + Members | backends[] |
| HealthMonitor | healthCheck |

Positive member `weight` values in the 1-100 range, including unequal values, are accepted for Octavia API compatibility and stored in the VirtualIP backend spec. Omitted weights default to `1`. The current VPP LB plugin path treats those weights as metadata only and does not apply them to live traffic distribution. Octavia `weight=0` is handled as draining and is not programmed as an active backend.

Octavia backup members are not supported. The driver rejects members with `backup=True` instead of treating them as active backends.

## Development

```bash
pip install -e ".[dev]"
python -m pytest tests/
```

## License

Apache-2.0
