# Octavia 運用ガイド

本ドキュメントは、OpenStack Octavia の arca provider driver を運用する際の状態確認、障害切り分け、復旧手順をまとめたものです。導入手順は [OpenStack Octavia 連携](./octavia.ja.md) を参照してください。

## 状態の見方

Octavia には経路広報専用のステータスがないため、arca driver は `VirtualIP` の状態を Octavia の `operating_status` に反映します。

| VirtualIP の状態 | Octavia の見え方 | 意味 |
|------------------|------------------|------|
| `Ready=True`, `RouteAdvertised=True`, 全 backend healthy | `ONLINE` | VIP は dataplane と経路広報の両方で利用可能 |
| `Ready=True`, `RouteAdvertised=True`, 一部 backend healthy | `DEGRADED` | VIP は利用可能だが backend の一部が異常 |
| `Ready=True`, healthy backend あり, `RouteAdvertised=False` または `Unknown` | `ERROR` | backend はあるが VIP 経路が広報されていない、または確認できない |
| backend なし | `OFFLINE` | pool/member がない、または無効化されているため意図的に待機中 |
| `Ready=False` かつ `reason` が `NoBackends` 以外 | `ERROR` | spec 不正などで提供不可 |

`RouteAdvertised` は VIP アドレス単位の状態です。同じ VIP アドレスに複数 listener がある場合、いずれかの listener に healthy backend があれば、その VIP アドレスの経路は広報されます。

主な `VirtualIP` Condition:

| Condition | 主な値 | 用途 |
|-----------|--------|------|
| `Ready` | `True`, `False` | operator が現在世代の VirtualIP spec を有効な設定として受け付けているか |
| `Serving` | `True`, `False` | この listener の address:port/protocol に healthy backend があるか |
| `RouteAdvertised` | `True`, `False`, `Unknown` | VIP アドレスの FRR 経路がこのノードで広報されているか |

`RouteAdvertised=Unknown` かつ `reason=RouteUpdateFailed` の場合、agent が FRR への経路追加または削除を `vtysh` 経由で実行しようとして失敗しています。この状態は Octavia 側では通常 `operating_status=ERROR` として見えます。

`Ready=True` は経路広報や dataplane 到達性の成功を単独では保証しません。Octavia で実際に提供中かを判断する場合は、`Ready` に加えて `Serving`、`RouteAdvertised`、backend health を確認してください。

## 初動確認

Octavia 側で対象ロードバランサーの状態を確認します。

```bash
openstack loadbalancer show <lb-id-or-name> \
  -c id -c name -c provisioning_status -c operating_status -c vip_address

openstack loadbalancer listener list --loadbalancer <lb-id-or-name>
```

Kubernetes 側で Octavia driver が作成した `VirtualIP` を確認します。

```bash
kubectl get virtualips -n arca-lb-system \
  -l app.kubernetes.io/managed-by=octavia-arca-driver \
  -o custom-columns='NAME:.metadata.name,ADDRESS:.spec.address,PORT:.spec.port,PROTOCOL:.spec.protocol,LB:.metadata.annotations.arca\.io/octavia-loadbalancer-id,LISTENER:.metadata.annotations.arca\.io/octavia-listener-id,HEALTHY:.status.healthyBackends,TOTAL:.status.totalBackends'
```

対象 `VirtualIP` の Condition を確認します。

```bash
kubectl describe virtualip -n arca-lb-system <virtualip-name>

kubectl get virtualip -n arca-lb-system <virtualip-name> \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.observedGeneration}{"\t"}{.message}{"\n"}{end}'
```

`RouteAdvertised` だけを確認する場合:

```bash
kubectl get virtualip -n arca-lb-system <virtualip-name> \
  -o jsonpath='{range .status.conditions[?(@.type=="RouteAdvertised")]}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

## route ERROR の切り分け

`RouteAdvertised=Unknown` / `RouteUpdateFailed` の場合は、まず agent ログを確認します。
以下の例では、Octavia が作成する `VirtualIP` と標準の arca-lb Agent
DaemonSet の両方が `arca-lb-system` にある単一 namespace 構成を前提にしています。

```bash
kubectl logs -n arca-lb-system \
  -l app.kubernetes.io/name=arca-lb-agent \
  --since=30m | grep -E 'failed to reconcile VIP address route|RouteUpdateFailed|vtysh|frr'
```

agent Pod と配置ノードを確認します。FRR はノードローカルに動作する前提のため、失敗しているノードを特定してください。

```bash
kubectl get pods -n arca-lb-system \
  -l app.kubernetes.io/name=arca-lb-agent \
  -o wide
```

対象 agent Pod から `vtysh` を実行できるか確認します。

```bash
AGENT_POD=<agent-pod-name>

kubectl exec -n arca-lb-system "$AGENT_POD" -- \
  /usr/bin/vtysh -c "show version"

kubectl exec -n arca-lb-system "$AGENT_POD" -- \
  /usr/bin/vtysh -c "show running-config"
```

IPv4 VIP の static route は通常次の形式で FRR に投入されます。IPv6 VIP の場合は `ipv6 route <vip>/128 Null0 tag <routeTag>` です。

```text
ip route <vip-address>/32 Null0 tag <routeTag>
```

`routeTag` のデフォルトは `10000` です。変更している場合は agent 設定の `routing.routeTag` を確認してください。

## よくある原因

- FRR が LB ノード上で起動していない。
- agent コンテナ内に `routing.vtyshPath` で指定した `vtysh` が存在しない。
- agent Pod にノードの `/run/frr` がマウントされていない、または FRR socket に接続できない。
- `vtysh` 実行ユーザーの権限やグループ設定が FRR socket に合っていない。
- FRR 設定が static route の追加を拒否している、または `vtysh` コマンドがタイムアウトしている。
- BGP peer や `redistribute static` の設定が不足し、FRR 内には route があっても外部へ広告されていない。
- agent が FRR のないノードにスケジュールされている。

## 復旧手順

1. 原因を修正します。FRR の起動、`/run/frr` マウント、socket 権限、agent の `routing.vtyshPath` / `routing.cmdTimeout`、BGP peer / `redistribute static` 設定を確認してください。
2. agent の安全網 reconcile を待ちます。デフォルトでは `agent.reconcileInterval` は `30s` です。次回 reconcile で経路追加に成功すると、`RouteAdvertised=True` になり、Octavia の `operating_status` は `ONLINE` または `DEGRADED` に戻ります。
3. すぐに再試行したい場合は、対象 `VirtualIP` に無害な annotation を付けて watch event を発生させます。

```bash
kubectl annotate virtualip -n arca-lb-system <virtualip-name> \
  arca.io/reconcile-at="$(date +%s)" --overwrite
```

復旧後、Condition と Octavia 状態を確認します。

```bash
kubectl get virtualip -n arca-lb-system <virtualip-name> \
  -o jsonpath='{range .status.conditions[?(@.type=="RouteAdvertised")]}{.status}{"\t"}{.reason}{"\n"}{end}'

openstack loadbalancer show <lb-id-or-name> \
  -c provisioning_status -c operating_status
```

## 手動 route 追加が必要な場合

通常は agent による自動復旧を優先してください。緊急回避として FRR に手動で static route を追加しても、agent が `vtysh` を実行できない限り `RouteAdvertised` は復旧せず、Octavia 側の `ERROR` 表示も残ります。

手動追加は、外部到達性を一時的に戻すための break-glass 操作としてのみ使用してください。

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

手動対応後も、必ず agent から `vtysh` が成功する状態へ戻してください。そうしないと、次回の listener 更新、backend 変更、agent 再起動、または route withdraw 時に Kubernetes / Octavia の状態と FRR の実状態が再びずれます。

## 注意事項

- `OFFLINE` は常に障害ではありません。backend が 0 件、draining 中、または admin_state_down により意図的に転送対象がない場合にも発生します。
- `RouteAdvertised=False` は「経路が不要な状態」のことがあります。`Serving=False` かつ healthy backend がない場合は、経路 withdraw が期待動作です。
- `RouteAdvertised=Unknown` / `RouteUpdateFailed` は運用対応が必要です。healthy backend があるのに VIP へ到達できない可能性があります。
- `observedGeneration` が `metadata.generation` と一致しない Condition は古い状態です。Octavia driver は古い世代の状態を Octavia に同期しません。
