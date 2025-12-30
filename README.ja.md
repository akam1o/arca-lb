# arca-lb

**arca-lb** は、VPP (Vector Packet Processing) の Layer 4 Load Balancer プラグインを制御する、中央集約型のロードバランサー管理システムです。シンプルな操作、VPP によるワイヤーレート、ECMP による水平スケーリングを実現します。

## Docker イメージ

- Controller: https://hub.docker.com/r/akam1o/arca-lb-controller
- Agent: https://hub.docker.com/r/akam1o/arca-lb-agent

## 特徴

- **中央集約管理**: REST API による VIP とバックエンドの統合管理
- **高性能データプレーン**: VPP の L4 LB プラグインによる高速パケット処理
- **柔軟なヘルスチェック**: HTTP/HTTPS、TCP、Ping プローブをサポート
- **自動経路広報**: FRR 連携による BGP 経路広報の自動制御
- **スケーラブル**: 複数の Agent による分散配置が可能

## アーキテクチャ

```
┌─────────────────────────────────────────┐
│         REST API Client                  │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│      Controller (REST + gRPC)           │
│  - VIP/Backend 管理                      │
│  - データストア (etcd/MySQL)              │
│  - Agent への設定配信                     │
└────────────┬────────────────────────────┘
             │ gRPC
             ▼
┌─────────────────────────────────────────┐
│      Agent (各ロードバランサーノード)     │
│  - VPP L4 LB 制御                        │
│  - ヘルスチェック実行                     │
│  - FRR BGP 経路広報制御                  │
└─────────────────────────────────────────┘
```

## プロジェクト構造

```
arca-lb/
├── cmd/                    # エントリポイント
│   ├── arcalb-controller/  # Controller バイナリ
│   └── arcalb-agent/       # Agent バイナリ
├── internal/               # 内部パッケージ
│   ├── controller/         # Controller 実装
│   ├── agent/             # Agent 実装
│   └── common/            # 共通パッケージ
├── pkg/                   # 外部公開 API
├── api/                   # API 定義
│   ├── proto/             # gRPC Protocol Buffers
│   └── openapi/           # REST API OpenAPI
├── deploy/                # デプロイメント設定
│   ├── docker/            # Dockerfile
│   ├── docker-compose/    # Docker Compose
│   └── kubernetes/        # Kubernetes manifests
├── test/                  # テスト
├── docs/                  # ドキュメント
└── migrations/            # データベースマイグレーション
```

## 必要要件

- **Go**: 1.24+ (開発環境)
- **VPP**: 24.10 (推奨, Agent 実行環境)
- **FRRouting**: 8.0+ (Agent 実行環境)
- **etcd**: 3.5+ (Controller, オプション)
- **MySQL**: 8.0+ (Controller, オプション)
- **Docker**: 20.10+ (オプション)

## クイックスタート

### 開発環境のセットアップ

1. リポジトリのクローン
```bash
git clone https://github.com/akam1o/arca-lb.git
cd arca-lb
```

2. 依存関係のインストール
```bash
make deps
```

3. 設定ファイルの準備
```bash
cp deploy/config/controller.example.yaml deploy/config/controller.yaml
cp deploy/config/agent.example.yaml deploy/config/agent.yaml
```

4. データストアの起動 (Docker Compose)
```bash
docker compose -f deploy/docker-compose/docker-compose.dev.yml up -d etcd
# または
docker compose -f deploy/docker-compose/docker-compose.dev.yml up -d mysql
```

5. ビルド
```bash
make build
```

### Controller の起動

```bash
./bin/arcalb-controller --config deploy/config/controller.yaml
```

### Agent の起動

```bash
export ARCA_AGENT_CONFIG=deploy/config/agent.yaml
sudo ./bin/arcalb-agent
```

**注意**: Agent は `--config` フラグではなく、`ARCA_AGENT_CONFIG` 環境変数で設定ファイルのパスを指定します（デフォルト: `/etc/arca-lb/agent.yaml`）。

## Makefile ターゲット

```bash
make help          # 使用可能なターゲットを表示
make deps          # 依存関係をダウンロード
make build         # バイナリをビルド
make test          # テストを実行
make lint          # コード品質チェック
make proto         # Protocol Buffers コード生成
make docker        # Docker イメージをビルド
make clean         # ビルド成果物を削除
```

## ドキュメント

詳細なドキュメントは `docs/` ディレクトリを参照してください：

### 運用ドキュメント
- [インストールガイド](docs/installation.md) - インストール手順とセットアップ
- [設定ガイド](docs/configuration.md) - Controller と Agent の設定方法
- [API リファレンス](docs/api.md) - REST API の詳細なリファレンス
- [トラブルシューティング](docs/troubleshooting.md) - よくある問題と解決方法
- [バックエンドサーバー設定ガイド](docs/backend-setup.md) - バックエンドサーバーの設定方法

### 開発者ドキュメント
- [アーキテクチャ](docs/architecture.md) - システムアーキテクチャと設計思想
- [開発環境セットアップ](docs/development.md) - 開発環境のセットアップとワークフロー
- [コントリビューションガイド](docs/contributing.md) - プロジェクトへの貢献方法

## お問い合わせ

お問い合わせは `arca-projects@ark-networks.net` までメール、または GitHub Issues で Issue を作成してください。

## ライセンス

Apache License 2.0
