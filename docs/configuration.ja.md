# arca-lb 設定ガイド

このドキュメントでは、arca-lb の設定方法を説明します。

## Controller 設定

Controller の設定ファイルは YAML 形式で、以下の構造を持ちます：

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
  level: "info"  # "debug", "info", "warn", "error"
  format: "json"  # "json" または "text"
```

### Server 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `host` | REST API サーバーのホスト | `0.0.0.0` |
| `port` | REST API サーバーのポート | `8080` |
| `read_timeout` | リクエスト読み込みタイムアウト | `10s` |
| `write_timeout` | レスポンス書き込みタイムアウト | `10s` |
| `read_header_timeout` | ヘッダー読み込みタイムアウト | `5s` |
| `idle_timeout` | アイドルタイムアウト | `60s` |
| `max_header_bytes` | 最大ヘッダーサイズ（バイト） | `1048576` (1MB) |
| `allowed_origins` | CORS 許可オリジン | `["http://localhost:3000"]` |

### gRPC 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `host` | gRPC サーバーのホスト | `0.0.0.0` |
| `port` | gRPC サーバーのポート | `50051` |
| `tls` | TLS 有効化 | `false` |

### DataStore 設定

#### etcd 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `endpoints` | etcd エンドポイントのリスト | `["http://localhost:2379"]` |
| `key_prefix` | キープレフィックス | `"/arca-lb"` |
| `tls` | TLS 有効化 | `false` |
| `cert_file` | TLS 証明書ファイル | - |
| `key_file` | TLS 秘密鍵ファイル | - |
| `ca_file` | CA 証明書ファイル | - |
| `dial_timeout` | 接続タイムアウト | `5s` |
| `request_timeout` | リクエストタイムアウト | `5s` |

#### MySQL 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `host` | MySQL ホスト | `localhost` |
| `port` | MySQL ポート | `3306` |
| `user` | MySQL ユーザー名 | - |
| `password` | MySQL パスワード | - |
| `database` | MySQL データベース名 | - |

### Log 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `level` | ログレベル | `info` |
| `format` | ログフォーマット | `json` |

## Agent 設定

Agent の設定ファイルは YAML 形式で、以下の構造を持ちます：

```yaml
agent:
  id: "agent-01"
  metadata:
    region: "us-west-1"
  reconcile_interval: "30s"
  heartbeat_interval: "10s"

controller:
  address: "localhost:50051"
  timeout: "10s"
  max_retries: 5
  retry_backoff: "1s"
  max_retry_backoff: "30s"
  tls:
    enabled: false
    cert_file: "/etc/arca-lb/certs/agent.crt"
    key_file: "/etc/arca-lb/certs/agent.key"
    ca_file: "/etc/arca-lb/certs/ca.crt"
    insecure_skip_verify: false

vpp:
  socket_path: "/run/vpp/api.sock"
  connect_timeout: "5s"
  reconnect_interval: "5s"
  max_reconnect_attempts: 0
  lb:
    encap_type: "GRE4"
    dscp: 1 # DSCP（L3DSR）方式のみ使用。L3DSR を使う場合は 1-63 を設定
    type: "CLUSTERIP"
    new_flows_table_length: 1024
    fail_on_all_backends_down: false

frr:
  enabled: true
  vtysh: "/usr/bin/vtysh"
  config_file: "/etc/frr/frr.conf"

health_check:
  worker_count: 4
  default_timeout: "3s"
  max_concurrent_checks: 100

metrics:
  enabled: true
  listen_address: "0.0.0.0:9090"
  path: "/metrics"
  timeout: "10s"

log:
  level: "info"
  format: "json"
  output: "stdout"
```

### Agent 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `agent.id` | Agent の一意な識別子 | ホスト名 |
| `agent.metadata` | Agent のメタデータ（キー・値のマップ） | - |
| `agent.reconcile_interval` | 設定ドリフトチェック間隔 | `30s` |
| `agent.heartbeat_interval` | ハートビート送信間隔 | `10s` |

### Controller 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `controller.address` | Controller の gRPC エンドポイント | `localhost:50051` |
| `controller.timeout` | gRPC 呼び出しのタイムアウト | `10s` |
| `controller.max_retries` | 接続試行の最大回数 | `5` |
| `controller.retry_backoff` | リトライの初期バックオフ | `1s` |
| `controller.max_retry_backoff` | リトライの最大バックオフ | `30s` |
| `controller.tls.enabled` | TLS を有効化 | `false` |
| `controller.tls.cert_file` | クライアント証明書ファイル | - |
| `controller.tls.key_file` | クライアント秘密鍵ファイル | - |
| `controller.tls.ca_file` | CA 証明書ファイル | - |
| `controller.tls.insecure_skip_verify` | サーバー証明書検証をスキップ | `false` |

### VPP 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `socket_path` | VPP API ソケットパス | `/run/vpp/api.sock` |
| `connect_timeout` | 接続タイムアウト | `5s` |
| `reconnect_interval` | 再接続間隔 | `5s` |
| `max_reconnect_attempts` | 最大再接続試行回数（0=無制限） | `0` |
| `lb.encap_type` | カプセル化タイプ（GRE4, GRE6, L3DSR, NAT4, NAT6） | `GRE4` |
| `lb.dscp` | DSCP（L3DSR）方式の DSCP 値（0-63。L3DSR を使う場合は 1-63 必須。GRE/NAT では未使用） | `0` |
| `lb.type` | ロードバランサーサービスタイプ（CLUSTERIP, NODEPORT） | `CLUSTERIP` |
| `lb.new_flows_table_length` | 新規接続のフローテーブルサイズ | `1024` |
| `lb.fail_on_all_backends_down` | すべてのバックエンドがダウン時に VIP 作成を失敗させる | `false` |

### FRR 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `enabled` | FRR 連携を有効化 | `false` |
| `vtysh` | vtysh コマンドのパス | `/usr/bin/vtysh` |
| `config_file` | FRR 設定ファイルのパス | `/etc/frr/frr.conf` |

### Health Check 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `worker_count` | 並行ヘルスチェックワーカー数 | `4` |
| `default_timeout` | ヘルスチェックのデフォルトタイムアウト | `3s` |
| `max_concurrent_checks` | ワーカーあたりの最大並行チェック数 | `100` |

### Metrics 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `enabled` | Prometheus メトリクスを有効化 | `false` |
| `listen_address` | メトリクスサーバーのリスンアドレス | `0.0.0.0:9090` |
| `path` | メトリクスエンドポイントの HTTP パス | `/metrics` |
| `timeout` | メトリクス HTTP サーバー操作のタイムアウト | `10s` |

### Log 設定

| パラメータ | 説明 | デフォルト |
|-----------|------|-----------|
| `level` | ログレベル | `info` |
| `format` | ログフォーマット | `json` |
| `output` | ログ出力先（`stdout`, `stderr`, またはファイルパス） | `stdout` |

## 環境変数による設定

### Controller

Controller は `--config` フラグで設定ファイルのパスを指定します：

```bash
./bin/arcalb-controller --config deploy/config/controller.yaml
```

### Agent

Agent は環境変数 `ARCA_AGENT_CONFIG` で設定ファイルのパスを指定します（デフォルト: `/etc/arca-lb/agent.yaml`）：

```bash
export ARCA_AGENT_CONFIG=/path/to/agent.yaml
./bin/arcalb-agent
```

以下の環境変数で設定を上書きできます：

- `ARCA_AGENT_ID` - Agent ID
- `ARCA_CONTROLLER_ADDRESS` - Controller の gRPC エンドポイント
- `ARCA_VPP_SOCKET` - VPP ソケットパス
- `ARCA_LOG_LEVEL` - ログレベル
- `ARCA_LOG_FORMAT` - ログフォーマット
- `ARCA_TLS_ENABLED` - TLS 有効化（true/false）
- `ARCA_TLS_CERT` - TLS 証明書ファイル
- `ARCA_TLS_KEY` - TLS 秘密鍵ファイル
- `ARCA_TLS_CA` - TLS CA 証明書ファイル

## 設定の検証

設定ファイルの構文エラーは起動時に検出されます。設定ファイルの構文を確認するには、実際に起動してみてください：

```bash
# Controller
./bin/arcalb-controller --config deploy/config/controller.yaml

# Agent
ARCA_AGENT_CONFIG=/path/to/agent.yaml ./bin/arcalb-agent
```

## 次のステップ

- [REST API リファレンス](./api.ja.md) を参照して、API の使用方法を確認します
- [トラブルシューティング](./troubleshooting.ja.md) を参照して、問題の解決方法を確認します
