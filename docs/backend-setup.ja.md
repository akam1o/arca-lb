# バックエンドサーバー設定ガイド

このドキュメントでは、arca-lb のバックエンドサーバーの設定方法を説明します。

## 概要

arca-lb は複数のカプセル化方式をサポートしています。バックエンドサーバーの設定は、Agent の `vpp.lb.encap_type` 設定によって異なります：

- **GRE4/GRE6**: GRE カプセル化を使用（MTU 調整が必要）
- **L3DSR**: Direct Server Return 方式（ARP 無効化が必要）
- **NAT4/NAT6**: NAT 方式（通常の設定で動作）

このドキュメントでは、L3DSR 方式と GRE4 方式の設定を説明します。

## Loopback IP 設定

VIP を loopback インターフェースに設定します。

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

### Linux (ip コマンド)

```bash
# ip addr add 192.168.1.100/32 dev lo
```

## ARP 設定

**L3DSR 方式の場合のみ必要**: VIP の ARP 応答を無効化します（L3DSR ではバックエンドが ARP に応答しない必要があるため）。

### Linux

```bash
# ARP 応答を無効化
echo 1 > /proc/sys/net/ipv4/conf/lo/arp_ignore
echo 2 > /proc/sys/net/ipv4/conf/lo/arp_announce

# 永続化（/etc/sysctl.conf）
net.ipv4.conf.lo.arp_ignore = 1
net.ipv4.conf.lo.arp_announce = 2
```

## RP Filter 設定

**L3DSR 方式の場合のみ必要**: Reverse Path Filtering を無効化します（L3DSR では非対称ルーティングが発生するため）。

### Linux

```bash
# RP Filter を無効化
echo 0 > /proc/sys/net/ipv4/conf/lo/rp_filter
echo 0 > /proc/sys/net/ipv4/conf/all/rp_filter
echo 0 > /proc/sys/net/ipv4/conf/default/rp_filter

# 永続化（/etc/sysctl.conf）
net.ipv4.conf.lo.rp_filter = 0
net.ipv4.conf.all.rp_filter = 0
net.ipv4.conf.default.rp_filter = 0
```

## MTU 設定

**GRE4/GRE6 方式の場合のみ必要**: GRE カプセル化に対応するため、MTU を調整します。GRE ヘッダーは約 24 バイトのオーバーヘッドがあるため、MTU を 1450 に設定することを推奨します。

### Linux

```bash
# MTU を 1450 に設定（GRE4 ヘッダー 24 バイトを考慮）
ip link set dev eth0 mtu 1450

# 永続化（/etc/network/interfaces または NetworkManager）
```

## OS 別設定例

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

## 設定の確認

### Loopback IP の確認

```bash
ip addr show lo
# または
ifconfig lo
```

### ARP 設定の確認

```bash
cat /proc/sys/net/ipv4/conf/lo/arp_ignore
cat /proc/sys/net/ipv4/conf/lo/arp_announce
```

### RP Filter 設定の確認

```bash
cat /proc/sys/net/ipv4/conf/lo/rp_filter
```

### MTU 設定の確認

```bash
ip link show eth0
# または
ifconfig eth0
```

## トラブルシューティング

### VIP にアクセスできない

1. Loopback IP が正しく設定されているか確認
2. ARP 設定が正しいか確認
3. RP Filter が無効化されているか確認

### パケットが破棄される

1. MTU 設定が適切か確認
2. ファイアウォールの設定を確認
3. ルーティングテーブルを確認

### 非対称ルーティングの問題

1. RP Filter が無効化されているか確認
2. ルーティングテーブルを確認
3. ネットワーク設定を確認

## セキュリティ考慮事項

- VIP は loopback インターフェースに設定し、外部から直接アクセスできないようにする
- ファイアウォールで適切に制限する
- ARP 応答を無効化することで、VIP のスプーフィングを防止

## 次のステップ

- [インストール手順](./installation.ja.md) を参照して、arca-lb をインストールします
- [設定ガイド](./configuration.ja.md) を参照して、詳細な設定を行います
