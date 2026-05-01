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

See [docs/octavia.md](../docs/octavia.md) for full documentation.

## Concept Mapping

| Octavia | arca-lb VirtualIP |
|---------|-------------------|
| Loadbalancer | VIP address |
| Listener | VirtualIP (port + protocol) |
| Pool + Members | backends[] |
| HealthMonitor | healthCheck |

Member `weight` is stored in the VirtualIP backend spec, but the current VPP LB plugin path does not apply it to live traffic distribution. Weighted backend programming will be enabled once the VPP LB API exposes backend weights.

## Development

```bash
pip install -e ".[dev]"
python -m pytest tests/
```

## License

Apache-2.0
