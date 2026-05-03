# Octavia Operations Guide

This document covers operational checks, troubleshooting, and recovery for the OpenStack Octavia arca provider driver. For installation and setup, see [OpenStack Octavia Integration](./octavia.md).

## Reading Status

Octavia does not have a route-specific status field, so the arca driver reflects `VirtualIP` route state through Octavia `operating_status`.

| VirtualIP state | Octavia view | Meaning |
|-----------------|--------------|---------|
| `Ready=True`, `RouteAdvertised=True`, all backends healthy | `ONLINE` | The VIP is available in both the dataplane and route plane |
| `Ready=True`, `RouteAdvertised=True`, some backends healthy | `DEGRADED` | The VIP is usable, but some backends are unhealthy |
| `Ready=True`, healthy backends exist, `RouteAdvertised=False` or `Unknown` | `ERROR` | Backends exist, but the VIP route is not advertised or cannot be confirmed |
| No backends | `OFFLINE` | The pool/member set is empty or disabled, so the VIP is intentionally idle |
| `Ready=False` with a reason other than `NoBackends` | `ERROR` | The VIP cannot be served because of an invalid spec or similar configuration issue |

`RouteAdvertised` is scoped to the VIP address. If multiple listeners share the same VIP address, the address route is advertised while at least one listener has a healthy backend.

Main `VirtualIP` conditions:

| Condition | Common values | Purpose |
|-----------|---------------|---------|
| `Ready` | `True`, `False` | Whether the operator accepts the current VirtualIP spec as valid configuration |
| `Serving` | `True`, `False` | Whether this listener address:port/protocol has healthy backends |
| `RouteAdvertised` | `True`, `False`, `Unknown` | Whether the VIP address route is advertised by this node |

`RouteAdvertised=Unknown` with `reason=RouteUpdateFailed` means the agent tried to add or remove the FRR route through `vtysh` and the route operation failed. In Octavia, this normally appears as `operating_status=ERROR`.

`Ready=True` alone does not prove that route advertisement or dataplane reachability succeeded. To decide whether Octavia traffic is actually being served, check `Ready`, `Serving`, `RouteAdvertised`, and backend health together.

## First Checks

Check the Octavia load balancer state.

```bash
openstack loadbalancer show <lb-id-or-name> \
  -c id -c name -c provisioning_status -c operating_status -c vip_address

openstack loadbalancer listener list --loadbalancer <lb-id-or-name>
```

Find `VirtualIP` resources created by the Octavia driver.

```bash
kubectl get virtualips -n arca-lb-system \
  -l app.kubernetes.io/managed-by=octavia-arca-driver \
  -o custom-columns='NAME:.metadata.name,ADDRESS:.spec.address,PORT:.spec.port,PROTOCOL:.spec.protocol,LB:.metadata.annotations.arca\.io/octavia-loadbalancer-id,LISTENER:.metadata.annotations.arca\.io/octavia-listener-id,HEALTHY:.status.healthyBackends,TOTAL:.status.totalBackends'
```

Inspect the target `VirtualIP` conditions.

```bash
kubectl describe virtualip -n arca-lb-system <virtualip-name>

kubectl get virtualip -n arca-lb-system <virtualip-name> \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.observedGeneration}{"\t"}{.message}{"\n"}{end}'
```

To inspect only `RouteAdvertised`:

```bash
kubectl get virtualip -n arca-lb-system <virtualip-name> \
  -o jsonpath='{range .status.conditions[?(@.type=="RouteAdvertised")]}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

## Troubleshooting Route ERROR

For `RouteAdvertised=Unknown` / `RouteUpdateFailed`, start with the agent logs.
The examples assume the standard single namespace setup, where both
Octavia-created `VirtualIP` resources and the arca-lb agent DaemonSet are in
`arca-lb-system`.

```bash
kubectl logs -n arca-lb-system \
  -l app.kubernetes.io/name=arca-lb-agent \
  --since=30m | grep -E 'failed to reconcile VIP address route|RouteUpdateFailed|vtysh|frr'
```

List agent Pods and their nodes. FRR is expected to run on the node, so identify which node is failing.

```bash
kubectl get pods -n arca-lb-system \
  -l app.kubernetes.io/name=arca-lb-agent \
  -o wide
```

Verify that `vtysh` works from the affected agent Pod.

```bash
AGENT_POD=<agent-pod-name>

kubectl exec -n arca-lb-system "$AGENT_POD" -- \
  /usr/bin/vtysh -c "show version"

kubectl exec -n arca-lb-system "$AGENT_POD" -- \
  /usr/bin/vtysh -c "show running-config"
```

For an IPv4 VIP, the static route normally has this form in FRR. For IPv6, it is `ipv6 route <vip>/128 Null0 tag <routeTag>`.

```text
ip route <vip-address>/32 Null0 tag <routeTag>
```

The default `routeTag` is `10000`. If you changed it, check `routing.routeTag` in the agent configuration.

## Common Causes

- FRR is not running on the LB node.
- The `vtysh` path configured by `routing.vtyshPath` does not exist inside the agent container.
- The node-local `/run/frr` directory is not mounted into the agent Pod, or the FRR socket is unreachable.
- The user or group used to run `vtysh` cannot access the FRR socket.
- FRR rejects the static route command, or the `vtysh` command times out.
- BGP peers or `redistribute static` are missing, so the route exists in FRR but is not advertised externally.
- The agent is scheduled on a node that does not have FRR.

## Recovery

1. Fix the underlying cause. Check FRR startup, the `/run/frr` mount, socket permissions, agent `routing.vtyshPath` / `routing.cmdTimeout`, BGP peers, and `redistribute static`.
2. Wait for the agent safety reconciliation. The default `agent.reconcileInterval` is `30s`. After the next successful route update, `RouteAdvertised=True` and Octavia `operating_status` returns to `ONLINE` or `DEGRADED`.
3. To retry immediately, add a harmless annotation to the affected `VirtualIP` to trigger a watch event.

```bash
kubectl annotate virtualip -n arca-lb-system <virtualip-name> \
  arca.io/reconcile-at="$(date +%s)" --overwrite
```

Verify recovery from both Kubernetes and Octavia.

```bash
kubectl get virtualip -n arca-lb-system <virtualip-name> \
  -o jsonpath='{range .status.conditions[?(@.type=="RouteAdvertised")]}{.status}{"\t"}{.reason}{"\n"}{end}'

openstack loadbalancer show <lb-id-or-name> \
  -c provisioning_status -c operating_status
```

## Manual Route Injection

Prefer automatic recovery through the agent. If you add the FRR static route manually while the agent still cannot run `vtysh`, external reachability may recover temporarily, but `RouteAdvertised` and Octavia `operating_status=ERROR` remain until the agent can complete route reconciliation.

Use manual route injection only as a break-glass operation.

```bash
# IPv4
vtysh -c "configure terminal" \
      -c "ip route <vip-address>/32 Null0 tag 10000" \
      -c "end"

# IPv6
vtysh -c "configure terminal" \
      -c "ipv6 route <vip-address>/128 Null0 tag 10000" \
      -c "end"
```

After manual intervention, restore the agent's ability to execute `vtysh`. Otherwise, listener changes, backend changes, agent restarts, or future route withdraw operations can make Kubernetes / Octavia state diverge from FRR again.

## Notes

- `OFFLINE` is not always a failure. It is expected when there are no backends, all members are draining, or admin_state_down leaves no active forwarding target.
- `RouteAdvertised=False` can be expected. If `Serving=False` and there are no healthy backends, route withdrawal is the intended behavior.
- `RouteAdvertised=Unknown` / `RouteUpdateFailed` requires operator action. If healthy backends exist, the VIP may be unreachable.
- Conditions whose `observedGeneration` does not match `metadata.generation` are stale. The Octavia driver does not sync stale-generation status back to Octavia.
