# arca-lb Troubleshooting

This document covers common arca-lb issues and how to resolve them.

## Controller Issues

### Controller does not start

**Symptoms**: Controller fails to start and shows an error.

**Causes and fixes**:

1. **Datastore connection error**
   - Ensure etcd or MySQL is running.
   - Verify connection settings.
   - Check network connectivity.

2. **Port already in use**
   - Ensure no other process uses ports 8080 or 50051.
   - Change ports in the config file.

3. **Config syntax error**
   - Validate YAML syntax.
   - Ensure required parameters are set.

### REST API not responding

**Symptoms**: REST API requests time out.

**Causes and fixes**:

1. **Controller not running**
   - Confirm the Controller process is running.
   - Check logs for errors.

2. **Firewall rules**
   - Verify port 8080 is open.
   - Review security group rules.

3. **Datastore issues**
   - Verify datastore connectivity.
   - Check `/readyz` for readiness.

## Agent Issues

### Agent does not start

**Symptoms**: Agent fails to start and shows an error.

**Causes and fixes**:

1. **VPP connection error**
   - Verify VPP is running: `systemctl status vpp`
   - Confirm VPP socket path: `/run/vpp/api.sock`
   - Check VPP logs: `journalctl -u vpp`

2. **Controller connection error**
   - Confirm the Controller gRPC endpoint is correct.
   - Check network connectivity.
   - Review firewall rules.

3. **Permission issue**
   - Check access to the VPP socket.
   - Run with `sudo` or add the user to the correct group.

### Config not applied to VPP

**Symptoms**: VIPs or backends are not reflected in VPP.

**Causes and fixes**:

1. **VPP connection problem**
   - Check Agent logs for VPP errors.
   - Verify VPP API availability.

2. **Reconciliation issues**
   - Check Agent logs for reconciliation errors.
   - Confirm reconciliation interval.

3. **VPP LB plugin issues**
   - Verify the VPP LB plugin is enabled.
   - Inspect VPP configuration.

### Health checks failing

**Symptoms**: Backend health checks fail.

**Causes and fixes**:

1. **Network connectivity**
   - Verify connectivity from Agent to backends.
   - Check firewall rules.

2. **Health check configuration**
   - Confirm health check settings (port, path, expected codes).

3. **Timeout settings**
   - Ensure timeouts are not too short.
   - Adjust for network latency.

### FRR BGP announcements not working

**Symptoms**: VIP routes are not announced.

**Causes and fixes**:

1. **FRR configuration**
   - Confirm FRR is running: `systemctl status frr`
   - Check FRR BGP config.
   - Ensure `vtysh` is available.

2. **Agent configuration**
   - Verify FRR integration is enabled.
   - Ensure `frr.vtysh` path is correct.

3. **BGP peer configuration**
   - Verify BGP peers are configured correctly.
   - Ensure BGP sessions are established.

## Metrics Issues

### Cannot scrape Prometheus metrics

**Symptoms**: Cannot access the `/metrics` endpoint.

**Causes and fixes**:

1. **Metrics disabled**
   - Ensure `metrics.enabled` is `true` in the Agent config.

2. **Port issues**
   - Verify the metrics port.
   - Check firewall rules.

3. **Metrics server not running**
   - Check Agent logs for metrics server errors.

## Viewing Logs

### Controller logs

```bash
# If logging to stdout
./bin/arcalb-controller --config deploy/config/controller.yaml

# If logging to a file
tail -f /var/log/arcalb-controller.log
```

### Agent logs

```bash
# If logging to stdout
export ARCA_AGENT_CONFIG=deploy/config/agent.yaml
sudo ./bin/arcalb-agent

# If logging to a file
tail -f /var/log/arcalb-agent.log
```

### VPP logs

```bash
# With systemd
journalctl -u vpp -f

# Direct log file
tail -f /var/log/vpp/vpp.log
```

### FRR logs

```bash
# With systemd
journalctl -u frr -f

# Direct log file
tail -f /var/log/frr/frr.log
```

## FAQ

### Q: Agent cannot connect to the Controller

**A**: Check the following:

1. Controller is running.
2. gRPC endpoint is correct.
3. Network connectivity is available.
4. Firewall rules allow the connection.

### Q: Traffic does not flow after creating a VIP

**A**: Check the following:

1. VIP is configured in VPP: `vppctl show lb vip`
2. Backends are added: `vppctl show lb as`
3. BGP routes are announced (if using FRR)
4. Backends are healthy and running

### Q: Health checks always fail

**A**: Check the following:

1. Backends are running.
2. Health check settings are correct (port, path, etc.).
3. Network connectivity is available.
4. Timeout settings are appropriate.

## Support

If the issue persists:

1. Search existing issues on [GitHub Issues](https://github.com/akam1o/arca-lb/issues).
2. Review logs to identify error messages.
3. Open a new issue to report the problem.
4. For security-related private reports, use [GitHub Security Advisories](https://github.com/akam1o/arca-lb/security/advisories/new).

## Next Steps

- Try reinstalling via the [Installation Guide](./installation.md)
- Review settings in the [Configuration Guide](./configuration.md)
