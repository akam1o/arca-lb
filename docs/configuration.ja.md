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
    socketPath: "/run/vpp/api.sock"

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
| `dataplane.vpp.socketPath` | VPP API ソケットパス | `/run/vpp/api.sock` |

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
  dscp: 10                   # 0-63 (L3DSR 用)
  backends:
    - address: 10.0.1.1
      weight: 100            # 1-100
    - address: 10.0.1.2
      weight: 100
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
| `dscp` | L3DSR モードの DSCP 値 (0-63) | いいえ |
| `backends` | バックエンドサーバーのリスト | いいえ |
| `healthCheck` | ヘルスチェック設定 | いいえ |

### Backend Spec フィールド

| フィールド | 説明 | 必須 |
|----------|------|------|
| `address` | バックエンド IP アドレス | はい |
| `weight` | トラフィック重み (1-100) | いいえ (デフォルト: 100) |

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

---

## 付録: Controller 設定 (v1、レガシー)

v1 Controller は YAML で設定します：

```yaml
server:
  host: "0.0.0.0"
  port: 8080

grpc:
  host: "0.0.0.0"
  port: 50051

datastore:
  type: "etcd"  # "etcd" または "mysql"
  etcd:
    endpoints:
      - "http://localhost:2379"
    key_prefix: "/arca-lb"
  mysql:
    host: "localhost"
    port: 3306
    user: "arcalb"
    password: "password"
    database: "arcalb"

log:
  level: "info"
  format: "json"
```

v1 Agent の設定については `deploy/config/agent.example.yaml` を参照してください。
