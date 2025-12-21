# Backend Server Setup Guide

This document explains how to configure backend servers for arca-lb.

## Overview

arca-lb supports multiple encapsulation methods. Backend configuration depends on the Agent `vpp.lb.encap_type` setting:

- **GRE4/GRE6**: Uses GRE encapsulation (requires MTU adjustment)
- **L3DSR**: Direct Server Return (requires disabling ARP responses)
- **NAT4/NAT6**: NAT-based (works with standard settings)

This guide covers L3DSR and GRE4 setups.

## Configure Loopback IP

Configure the VIP on the loopback interface.

### Linux (systemd-networkd)

```bash
# /etc/systemd/network/lo-vip.network
[Match]
Name=lo

[Network]
Address=192.168.1.100/32
```

### Linux (NetworkManager)

```bash
# nmcli connection modify lo +ipv4.addresses 192.168.1.100/32
# nmcli connection up lo
```

### Linux (ifconfig)

```bash
# ifconfig lo:0 192.168.1.100 netmask 255.255.255.255 up
```

### Linux (ip command)

```bash
# ip addr add 192.168.1.100/32 dev lo
```

## ARP Configuration

**L3DSR only**: Disable ARP replies for the VIP (backends must not answer ARP in L3DSR).

### Linux

```bash
# Disable ARP replies
echo 1 > /proc/sys/net/ipv4/conf/lo/arp_ignore
echo 2 > /proc/sys/net/ipv4/conf/lo/arp_announce

# Persist (/etc/sysctl.conf)
net.ipv4.conf.lo.arp_ignore = 1
net.ipv4.conf.lo.arp_announce = 2
```

## RP Filter Configuration

**L3DSR only**: Disable Reverse Path Filtering to allow asymmetric routing.

### Linux

```bash
# Disable RP filter
echo 0 > /proc/sys/net/ipv4/conf/lo/rp_filter
echo 0 > /proc/sys/net/ipv4/conf/all/rp_filter
echo 0 > /proc/sys/net/ipv4/conf/default/rp_filter

# Persist (/etc/sysctl.conf)
net.ipv4.conf.lo.rp_filter = 0
net.ipv4.conf.all.rp_filter = 0
net.ipv4.conf.default.rp_filter = 0
```

## MTU Configuration

**GRE4/GRE6 only**: Adjust MTU for GRE encapsulation. GRE headers add roughly 24 bytes of overhead; setting MTU to 1450 is recommended.

### Linux

```bash
# Set MTU to 1450 (accounts for ~24 bytes GRE4 overhead)
ip link set dev eth0 mtu 1450

# Persist (/etc/network/interfaces or NetworkManager)
```

## OS Examples

### Ubuntu/Debian

```bash
# /etc/network/interfaces
auto lo
iface lo inet loopback
    up ip addr add 192.168.1.100/32 dev lo
    up sysctl -w net.ipv4.conf.lo.arp_ignore=1
    up sysctl -w net.ipv4.conf.lo.arp_announce=2
    up sysctl -w net.ipv4.conf.lo.rp_filter=0
```

### CentOS/RHEL

```bash
# /etc/sysconfig/network-scripts/ifcfg-lo:0
DEVICE=lo:0
IPADDR=192.168.1.100
NETMASK=255.255.255.255
ONBOOT=yes

# /etc/sysctl.conf
net.ipv4.conf.lo.arp_ignore = 1
net.ipv4.conf.lo.arp_announce = 2
net.ipv4.conf.lo.rp_filter = 0
```

### systemd-networkd

```bash
# /etc/systemd/network/lo-vip.network
[Match]
Name=lo

[Network]
Address=192.168.1.100/32

[Link]
MTU=1500
```

## Validation

### Verify loopback IP

```bash
ip addr show lo
# or
ifconfig lo
```

### Verify ARP settings

```bash
cat /proc/sys/net/ipv4/conf/lo/arp_ignore
cat /proc/sys/net/ipv4/conf/lo/arp_announce
```

### Verify RP filter settings

```bash
cat /proc/sys/net/ipv4/conf/lo/rp_filter
```

### Verify MTU

```bash
ip link show eth0
# or
ifconfig eth0
```

## Troubleshooting

### Cannot reach the VIP

1. Confirm the loopback IP is configured correctly.
2. Confirm ARP settings are correct.
3. Confirm RP filter is disabled.

### Packets are dropped

1. Check MTU settings.
2. Verify firewall rules.
3. Check the routing table.

### Asymmetric routing issues

1. Confirm RP filter is disabled.
2. Check routing tables.
3. Validate network settings.

## Security Considerations

- Configure VIPs on the loopback interface so they are not directly reachable from outside.
- Restrict access with firewall rules.
- Disabling ARP replies helps prevent VIP spoofing.

## Next Steps

- See [Installation](./installation.md) to install arca-lb
- See the [Configuration Guide](./configuration.md) for detailed settings
