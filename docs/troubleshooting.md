# arca-lb Troubleshooting

This document covers common arca-lb issues and how to resolve them.

## Operator Issues (v2)

### Operator does not start

**Symptoms**: Operator Pod is in CrashLoopBackOff.

**Causes and fixes**:

1. **CRD not installed**
   - Install CRDs: `kubectl apply -f config/crd/bases/`
   - Verify: `kubectl get crd virtualips.arca.io`

2. **RBAC misconfiguration**
   - Apply RBAC: `kubectl apply -f config/rbac/`
   - Check ServiceAccount permissions.

3. **Image not found**
   - Verify image name and tag in the Deployment.
   - Pull manually: `docker pull <image>`

### VirtualIP status not updating

**Symptoms**: VirtualIP shows no status or stale status.

**Causes and fixes**:

1. **Operator not running**
   - Check Pod status: `kubectl get pods -l app=arcalb-operator`
   - Review logs: `kubectl logs -l app=arcalb-operator`

2. **RBAC issue**
   - Operator needs `update` on `virtualips/status`.
   - Check `config/rbac/role.yaml`.

3. **Finalizer stuck**
   - Inspect metadata: `kubectl get vip <name> -o yaml`
   - Remove finalizer if needed: `kubectl edit vip <name>`

### Webhook validation failing

**Symptoms**: `kubectl apply` returns admission error.

**Causes and fixes**:

1. **Invalid field values**
   - Check error message for specific field.
   - See [API Reference](./api.md) for valid values.

2. **Webhook not registered**
   - Verify the webhook config exists.
   - Ensure cert-manager or webhook certificates are valid.

## Agent Issues

### Agent does not start

**Symptoms**: Agent Pod is in CrashLoopBackOff or fails to start.

**Causes and fixes**:

1. **Config file not found**
   - Verify ConfigMap mount at `/etc/arca-lb/agent.yaml`.

2. **Kubernetes API connection failure**
   - Verify RBAC for the Agent ServiceAccount.
   - Check kubeconfig/in-cluster auth.

3. **VPP connection error**
   - Verify VPP is running: `systemctl status vpp`
   - Confirm VPP socket path: `/run/vpp/api.sock`
   - Check socket mount in the DaemonSet spec.

4. **Permission issue**
   - Agent needs access to the VPP socket.
   - Ensure the Pod runs with appropriate privileges.

### VIP not applied to VPP

**Symptoms**: VirtualIP is created in Kubernetes but not applied to VPP.

**Causes and fixes**:

1. **Watcher not receiving events**
   - Check Agent logs for watch errors.
   - Verify `kubernetes.watchNamespaces` config.
   - Verify `kubernetes.labelSelector` config.

2. **Reconciler errors**
   - Check Agent logs for reconciliation errors.
   - Look for DataPlane/Router errors.

3. **VPP LB plugin issues**
   - Verify the VPP LB plugin is enabled.
   - Inspect VPP config: `vppctl show lb vip`

### Health checks failing

**Symptoms**: Backend health checks report failures.

**Causes and fixes**:

1. **Network connectivity**
   - Verify connectivity from Agent to backends.
   - Check firewall rules.

2. **Health check configuration**
   - Confirm health check settings in the VirtualIP CR (port, path, expected codes).
   - Review VirtualIP status: `kubectl get vip <name> -o yaml`

3. **Timeout settings**
   - Ensure timeouts are not too short.
   - Adjust `healthCheck.timeoutSeconds` in the VirtualIP spec.

### FRR BGP announcements not working

**Symptoms**: VIP routes are not announced via BGP.

**Causes and fixes**:

1. **FRR not running**
   - Confirm FRR is running: `systemctl status frr`
   - Ensure `routing.type: frr` in Agent config.

2. **vtysh not found**
   - Verify `routing.frr.vtyshPath` in Agent config.
   - Default: `/usr/bin/vtysh`

3. **BGP peer configuration**
   - Verify BGP peers are configured correctly.
   - Check: `vtysh -c "show bgp summary"`

## Metrics Issues

### Cannot scrape Prometheus metrics

**Symptoms**: Cannot access the `/metrics` endpoint.

**Causes and fixes**:

1. **Metrics disabled**
   - Ensure `metrics.enabled: true` in Agent config.

2. **Port issues**
   - Verify the metrics port (default: `:9090`).
   - Check firewall rules.

3. **Metrics server not running**
   - Check Agent logs for metrics server errors.

## Viewing Logs

### Operator logs

```bash
# Stream operator logs
kubectl logs -f -l app=arcalb-operator

# With increased verbosity
kubectl logs -f -l app=arcalb-operator -- --zap-log-level=debug
```

### Agent logs (v2)

```bash
# Stream agent logs (Kubernetes)
kubectl logs -f -l app=arcalb-agent

# Standalone agent
sudo ./bin/arcalb-agent --config deploy/config/agent.example.yaml
```

### VPP logs

```bash
# With systemd
journalctl -u vpp -f

# Direct log file
tail -f /var/log/vpp/vpp.log

# Check VPP state
vppctl show lb vip verbose
vppctl show lb as
```

### FRR logs

```bash
# With systemd
journalctl -u frr -f

# Direct log file
tail -f /var/log/frr/frr.log
```

## Debugging with kubectl

```bash
# List all VIPs with status
kubectl get vip -o wide

# Inspect specific VIP
kubectl get vip web-vip -o yaml

# Watch for changes
kubectl get vip -w

# Describe for events
kubectl describe vip web-vip

# Check Agent pods
kubectl get pods -l app=arcalb-agent -o wide

# Check Operator pods
kubectl get pods -l app=arcalb-operator
```

## FAQ

### Q: VirtualIP stays in "Not Ready" state

**A**: Check the following:

1. Agent is running and watching the correct namespace.
2. VPP connection is healthy (Agent logs).
3. Backends are healthy (check VIP status).
4. Check Operator logs for reconciliation errors.

### Q: Traffic does not flow after creating a VIP

**A**: Check the following:

1. VIP is configured in VPP: `vppctl show lb vip`
2. Backends are added: `vppctl show lb as`
3. BGP routes are announced (if using FRR): `vtysh -c "show ip route"`
4. Backends are healthy and running.
5. Backend server is configured correctly (see [Backend Setup](./backend-setup.md)).

### Q: How do I migrate from the legacy controller?

**A**:

1. Install CRDs: `kubectl apply -f config/crd/bases/`
2. Deploy the Operator: `kubectl apply -f config/manager/manager.yaml`
3. Convert legacy VIP definitions to VirtualIP CRDs and apply.
4. Deploy the Agent DaemonSet.
5. Decommission the legacy Controller.

---

## Legacy Troubleshooting (v1)

<details>
<summary>v1 Controller / Agent issues</summary>

### Controller does not start

1. Verify datastore (etcd/MySQL) is running.
2. Check ports 8080 / 50051 are free.
3. Validate config YAML syntax.

### Agent does not connect to Controller

1. Verify the Controller gRPC endpoint is correct.
2. Check network connectivity.
3. Review firewall rules.

### REST API not responding

1. Confirm Controller is running.
2. Check port 8080 / firewall rules.
3. Check datastore connectivity.

</details>
