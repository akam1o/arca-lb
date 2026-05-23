# arca-lb 設定ガイド

このドキュメントでは、arca-lb の設定方法を説明します。

## Agent 設定 (v2)

Agent は YAML 設定ファイルから設定を読み込みます。パスは `--config` フラグまたは `ARCA_AGENT_CONFIG` 環境変数で指定します（デフォルト: `/etc/arca-lb/agent.yaml`）。

以下の例では、本番向けの VPP データプレーンと FRR routing を明示的に有効化しています。省略時、これらのバックエンドは `noop` になります。

```yaml
agent:
  id: "agent-01"
  storePath: "/var/lib/arca-lb/agent.db"
  reconcileInterval: "30s"

kubernetes:
  kubeconfig: ""           # 空 = クラスター内設定を使用
  namespace: ""            # 空 = すべての namespace を監視
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
  routeTag: 10000
  cmdTimeout: "10s"

rollout:
  enabled: true
  leaseNamespace: "arca-lb-system"
  leaseDuration: "2m"
  retryInterval: "1s"

healthCheck:
  workerCount: 4
  maxConcurrentChecks: 64
  defaultTimeout: "3s"

metrics:
  enabled: true
  address: ":9090"
  path: "/metrics"

telemetry:
  otlpEndpoint: ""         # 空 = 無効
  otlpInsecure: false      # plaintext collector の場合のみ true

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
| `kubernetes.namespace` | VirtualIP リソースを監視するネームスペース（空の場合はすべての namespace を監視） | `""` |
| `kubernetes.resyncInterval` | Informer の再同期間隔 | `30s` |

### DataPlane 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `dataplane.type` | データプレーンバックエンド (`vpp` または `noop`) | `noop` |
| `dataplane.vpp.socket_path` | VPP API ソケットパス | `/run/vpp/api.sock` |
| `dataplane.vpp.retained_vip_tuning_drift_policy` | retained VIP の tuning drift の扱い (`rolling_recreate` または `preserve`) | `rolling_recreate` |
| `dataplane.vpp.retained_vip_tuning_drift_drain` | tuning drift がある retained VIP を再作成する前の drain 時間 | `30s` |

### Routing 設定

`routing.type` が `frr` の場合、Agent は `vtysh` 経由で VIP の static route を管理します。Kubernetes デプロイでは FRR はノード上で稼働している前提で、Agent は `/run/frr` の runtime socket directory をマウントして利用します。Agent は FRR プロセスの起動や BGP peer の設定は行いません。

同梱の `config/agent/daemonset.yaml` は FRR 必須の本番向けマニフェストです。開発用途やデータプレーンのみの検証で `routing.enabled: false` または `routing.type: noop` が必要な場合は、Pod が `vtysh` を待機せず `/run/frr` もマウントしない `config/agent-no-frr/` をデプロイしてください。

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `routing.enabled` | BGP 経路管理を有効化 | `false` |
| `routing.type` | Router バックエンド (`frr` または `noop`) | `noop` |
| `routing.vtyshPath` | vtysh コマンドのパス | `/usr/bin/vtysh` |
| `routing.routeTag` | Static Route のタグ値 | `10000` |
| `routing.cmdTimeout` | vtysh コマンドのタイムアウト | `10s` |

### Rollout 設定

有効な場合、Agent は Kubernetes `Lease` を使い、VIP address 変更や retained VIP の rolling recreate などの disruptive な変更をクラスター全体で直列化します。

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `rollout.enabled` | クラスター全体の rollout 直列化を有効化 | `false` |
| `rollout.leaseNamespace` | rollout Lease を作成する namespace | Pod namespace、その後 `arca-lb-system` |
| `rollout.leaseDuration` | 他 Agent が Lease を引き継げるまでの時間 | `2m` |
| `rollout.retryInterval` | 他 Agent が Lease を保持している間の待機間隔 | `1s` |

### HealthCheck 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `healthCheck.workerCount` | ヘルスチェックワーカー goroutine 数 | `4` |
| `healthCheck.maxConcurrentChecks` | ワーカーあたりの最大並行チェック数 | `64` |
| `healthCheck.defaultTimeout` | デフォルトのプローブタイムアウト | `3s` |

### Metrics 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `metrics.enabled` | Prometheus メトリクスエンドポイントを有効化 | `false` |
| `metrics.address` | メトリクスサーバーのリスンアドレス | `:9090` |
| `metrics.path` | メトリクスエンドポイントの HTTP パス | `/metrics` |

### Telemetry 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `telemetry.otlpEndpoint` | OTLP コレクターエンドポイント（空 = 無効） | `""` |
| `telemetry.otlpInsecure` | TLS を使わない plaintext OTLP collector へトレースを送信する | `false` |

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
| `--enable-webhooks` | Admission Webhook を有効化 | `true` |
| `--leader-elect` | Leader Election を有効化 | `false` |

## Controller Secret ファイル

Controller は機密値をファイルから読み込めます。これにより、Kubernetes
Secret をマウントし、YAML 設定ファイルに secret material を直接書かずに
運用できます。直接値と対応する file フィールドは同時に指定しないでください。

| パラメータ | 説明 |
|-----------|------|
| `server.api_key_file` | REST API key を含むファイル |
| `grpc.api_key_file` | gRPC API key を含むファイル |
| `datastore.mysql.password_file` | MySQL password を含むファイル |

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
    type: http               # http, https, tcp, ping, tls-hello
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
| `type` | プローブタイプ (http, https, tcp, ping, tls-hello) | はい |
| `intervalSeconds` | プローブ間隔（秒） | いいえ (デフォルト: 5) |
| `timeoutSeconds` | プローブタイムアウト（秒） | いいえ (デフォルト: 3) |
| `riseCount` | 健全と判定する連続成功回数 | いいえ (デフォルト: 3) |
| `fallCount` | 不健全と判定する連続失敗回数 | いいえ (デフォルト: 2) |
| `http` | HTTP/HTTPS プローブ設定 | いいえ |
| `tcp` | TCP プローブ設定 | いいえ |

ヘルスチェックの時間設定は、現在は秒単位のフィールドです。将来の API/model revision でミリ秒単位の interval/timeout に対応する予定です。現行バージョンでは秒単位の整数値を指定してください。

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
