# arca-lb 設定ガイド

このドキュメントでは、arca-lb の設定方法を説明します。

## Agent 設定 (v2)

Agent は YAML 設定ファイルから設定を読み込みます。パスは `--config` フラグまたは `ARCA_AGENT_CONFIG` 環境変数で指定します（デフォルト: `/etc/arca-lb/agent.yaml`）。

```yaml
agent:
  id: "agent-01"
  storePath: "/var/lib/arca-lb/agent.db"
  reconcileInterval: "30s"

kubernetes:
  kubeconfig: ""           # 空 = クラスター内設定を使用
  namespace: "default"
  resyncInterval: "30s"

dataplane:
  type: "vpp"              # "vpp" または "noop"
  vpp:
    socket_path: "/run/vpp/api.sock"
    retained_vip_tuning_drift_policy: "rolling_recreate"
    retained_vip_tuning_drift_drain: "30s"

routing:
  enabled: true
  type: "frr"              # "frr" または "noop"
  vtyshPath: "/usr/bin/vtysh"
  routeTag: 100
  cmdTimeout: "5s"

healthCheck:
  workerCount: 4
  maxConcurrentChecks: 100
  defaultTimeout: "3s"

metrics:
  enabled: true
  address: "0.0.0.0:9090"
  path: "/metrics"

telemetry:
  otlpEndpoint: ""         # 空 = 無効

log:
  level: "info"            # "debug", "info", "warn", "error"
  format: "json"           # "json" または "text"
```

### Agent 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `agent.id` | Agent の一意な識別子 | ホスト名 |
| `agent.storePath` | bbolt データベースファイルのパス | `/var/lib/arca-lb/agent.db` |
| `agent.reconcileInterval` | 定期的な Reconcile の間隔 | `30s` |

### Kubernetes 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `kubernetes.kubeconfig` | kubeconfig ファイルのパス（空 = クラスター内設定） | `""` |
| `kubernetes.namespace` | VirtualIP リソースを監視するネームスペース | `default` |
| `kubernetes.resyncInterval` | Informer の再同期間隔 | `30s` |

### DataPlane 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `dataplane.type` | データプレーンバックエンド (`vpp` または `noop`) | `vpp` |
| `dataplane.vpp.socket_path` | VPP API ソケットパス | `/run/vpp/api.sock` |
| `dataplane.vpp.retained_vip_tuning_drift_policy` | retained VIP の tuning drift の扱い (`rolling_recreate` または `preserve`) | `rolling_recreate` |
| `dataplane.vpp.retained_vip_tuning_drift_drain` | tuning drift がある retained VIP を再作成する前の drain 時間 | `30s` |

### Routing 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `routing.enabled` | BGP 経路管理を有効化 | `false` |
| `routing.type` | Router バックエンド (`frr` または `noop`) | `frr` |
| `routing.vtyshPath` | vtysh コマンドのパス | `/usr/bin/vtysh` |
| `routing.routeTag` | Static Route のタグ値 | `100` |
| `routing.cmdTimeout` | vtysh コマンドのタイムアウト | `5s` |

### HealthCheck 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `healthCheck.workerCount` | ヘルスチェックワーカー goroutine 数 | `4` |
| `healthCheck.maxConcurrentChecks` | ワーカーあたりの最大並行チェック数 | `100` |
| `healthCheck.defaultTimeout` | デフォルトのプローブタイムアウト | `3s` |

### Metrics 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `metrics.enabled` | Prometheus メトリクスエンドポイントを有効化 | `false` |
| `metrics.address` | メトリクスサーバーのリスンアドレス | `0.0.0.0:9090` |
| `metrics.path` | メトリクスエンドポイントの HTTP パス | `/metrics` |

### Telemetry 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `telemetry.otlpEndpoint` | OTLP コレクターエンドポイント（空 = 無効） | `""` |

### Log 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `log.level` | ログレベル | `info` |
| `log.format` | ログフォーマット (`json` または `text`) | `json` |

## Operator 設定

Operator はコマンドラインフラグで設定します：

| フラグ | 説明 | デフォルト |
|------|------|-----------|
| `--metrics-bind-address` | メトリクスエンドポイントのアドレス | `:8080` |
| `--health-probe-bind-address` | ヘルスプローブエンドポイントのアドレス | `:8081` |
| `--enable-webhooks` | Admission Webhook を有効化 | `false` |
| `--leader-elect` | Leader Election を有効化 | `false` |

## VirtualIP CRD 設定

VIP は Kubernetes Custom Resource として設定します：

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: web-vip
  namespace: default
spec:
  address: 203.0.113.10
  port: 80
  protocol: TCP             # TCP または UDP
  encapType: L3DSR           # GRE4, GRE6, L3DSR, NAT4, NAT6
  dscp: 10                   # 任意の DSCP override、1-63 (DSCP ベース L3DSR 用)
  backends:
    - address: 10.0.1.1
      weight: 1              # 1-100
    - address: 10.0.1.2
      weight: 1
  healthCheck:
    type: http               # http, https, tcp, ping
    intervalSeconds: 5
    timeoutSeconds: 3
    riseCount: 3
    fallCount: 2
    http:
      port: 8080
      path: /healthz
      method: GET
      expectedCodes: [200]
```

### VirtualIP Spec フィールド

| フィールド | 説明 | 必須 |
|----------|------|------|
| `address` | 仮想 IP アドレス | はい |
| `port` | 仮想ポート (1-65535) | はい |
| `protocol` | トランスポートプロトコル (TCP, UDP) | はい |
| `encapType` | カプセル化タイプ (GRE4, GRE6, L3DSR, NAT4, NAT6) | いいえ (デフォルト: L3DSR) |
| `dscp` | DSCP ベース L3DSR 用の任意 DSCP override (指定時 1-63。省略時は Agent の既定値) | いいえ |
| `backends` | バックエンドサーバーのリスト | いいえ |
| `healthCheck` | ヘルスチェック設定 | いいえ |

### Backend Spec フィールド

| フィールド | 説明 | 必須 |
|----------|------|------|
| `address` | バックエンド IP アドレス | はい |
| `weight` | 希望するトラフィック重み (1-100)。デフォルト: 1。正の不均等な weight も受け付けて backend spec に保存しますが、現在の VPP LB plugin 経路では metadata のみです。全 backend は weight なしで programming され、実トラフィック分散は重み付きになりません。 | いいえ |

### HealthCheck Spec フィールド

| フィールド | 説明 | 必須 |
|----------|------|------|
| `type` | プローブタイプ (http, https, tcp, ping) | はい |
| `intervalSeconds` | プローブ間隔（秒） | いいえ (デフォルト: 5) |
| `timeoutSeconds` | プローブタイムアウト（秒） | いいえ (デフォルト: 3) |
| `riseCount` | 健全と判定する連続成功回数 | いいえ (デフォルト: 3) |
| `fallCount` | 不健全と判定する連続失敗回数 | いいえ (デフォルト: 2) |
| `http` | HTTP/HTTPS プローブ設定 | いいえ |
| `tcp` | TCP プローブ設定 | いいえ |

## 環境変数による設定

### Agent (v2)

```bash
# 設定ファイルのパスを指定
./bin/arcalb-agent --config /path/to/agent.yaml

# または環境変数で指定
export ARCA_AGENT_CONFIG=/path/to/agent.yaml
./bin/arcalb-agent
```

## 次のステップ

- [API リファレンス](./api.ja.md) を参照して、VirtualIP CRD スキーマを確認します
- [トラブルシューティング](./troubleshooting.ja.md) を参照して、問題の解決方法を確認します
