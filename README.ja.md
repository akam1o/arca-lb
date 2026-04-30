# arca-lb

**arca-lb** は、VPP (Vector Packet Processing) の Layer 4 Load Balancer プラグインを制御する、Kubernetes ネイティブなロードバランサー管理システムです。CRD による宣言的な設定、VPP によるワイヤーレート、ECMP による水平スケーリングを実現します。

## Docker イメージ

- Operator: `ghcr.io/akam1o/arca-lb-operator`
- Agent: `ghcr.io/akam1o/arca-lb-agent`

## 特徴

- **Kubernetes ネイティブ**: `VirtualIP` Custom Resource (CRD) による宣言的な VIP 管理
- **Operator パターン**: Kubernetes Operator によるバリデーション、ステータス管理、ライフサイクル制御
- **高性能データプレーン**: VPP の L4 LB プラグインによるワイヤーレートパケット処理
- **プラガブルインターフェース**: テスト容易性と拡張性のための DataPlane / Router インターフェース
- **柔軟なヘルスチェック**: VIP ごとの HTTP/HTTPS、TCP、Ping プローブ設定
- **自動経路広報**: FRR 連携による BGP 経路広報の自動制御
- **スケーラブル**: LB ノードごとに 1 Agent、DaemonSet として配置
- **可観測性**: OpenTelemetry トレース/メトリクス、Prometheus エンドポイント、構造化ログ
- **OpenStack Octavia 連携**: Octavia プロバイダードライバーによる OpenStack LBaaS API 統合

## アーキテクチャ

```
┌─────────────────────────────────────────┐
│           kubectl / GitOps              │
│   (VirtualIP CRD マニフェスト適用)       │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│      Kubernetes API Server              │
│  - VirtualIP CRD (arca.io/v1alpha1)     │
│  - Admission Webhook バリデーション      │
└──────┬──────────────────────┬───────────┘
       │                      │
       ▼                      ▼
┌──────────────┐   ┌──────────────────────┐
│   Operator   │   │  Agent (各 LB ノード) │
│  - Reconcile │   │  - K8s Informer      │
│  - Status    │   │  - VIP 別 Reconciler  │
│  - Webhook   │   │  - ヘルスチェック     │
└──────────────┘   │  - VPP DataPlane     │
                   │  - FRR Router        │
                   │  - bbolt ローカル保存  │
                   │  - OTel テレメトリ    │
                   └──────────────────────┘
```

### VirtualIP Custom Resource の例

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: web-vip
spec:
  address: 203.0.113.10
  port: 80
  protocol: TCP
  encapType: L3DSR
  dscp: 10
  backends:
    - address: 10.0.1.1
      weight: 100
    - address: 10.0.1.2
      weight: 100
  healthCheck:
    type: http
    intervalSeconds: 5
    timeoutSeconds: 3
    riseCount: 3
    fallCount: 2
    http:
      port: 8080
      path: /healthz
```

## プロジェクト構造

```
arca-lb/
├── api/
│   └── v1alpha1/           # VirtualIP CRD 型定義 (kubebuilder)
├── cmd/
│   ├── operator/           # Operator (K8s コントローラー) バイナリ
│   ├── agent/              # Agent バイナリ
│   ├── arcalb-controller/  # v1 Controller バイナリ (レガシー)
│   └── arcalb-agent/       # v1 Agent バイナリ (レガシー)
├── config/                 # K8s マニフェスト (生成 + 手書き)
│   ├── crd/                # CRD YAML (controller-gen 出力)
│   ├── rbac/               # RBAC ロール
│   ├── manager/            # Operator Deployment
│   ├── agent/              # Agent DaemonSet
│   └── samples/            # VirtualIP リソースの例
├── internal/
│   ├── operator/           # Operator リコンサイラー + Webhook
│   ├── agent/              # Agent 実装
│   │   ├── config/         # Agent 設定
│   │   ├── dataplane/      # DataPlane インターフェース (VPP, Noop)
│   │   ├── routing/        # Router インターフェース (FRR, Noop)
│   │   ├── store/          # bbolt ローカル永続化
│   │   ├── watcher/        # K8s Informer ベースの CRD ウォッチャー
│   │   ├── reconciler/     # VIP 別リコンサイラー
│   │   └── healthcheck/    # ヘルスチェックエンジン
│   ├── pkg/otel/           # OpenTelemetry セットアップ
│   └── common/             # 共通モデル (v1)
├── octavia-driver/         # OpenStack Octavia プロバイダードライバー (Python)
├── deploy/                 # デプロイメント設定
├── docs/                   # ドキュメント
└── test/                   # テスト
```

## 必要要件

- **Go**: 1.24+ (開発環境)
- **Kubernetes**: 1.28+ (実行環境)
- **VPP**: 24.10 (推奨, Agent 実行環境)
- **FRRouting**: 8.0+ (Agent 実行環境, オプション)
- **controller-gen**: CRD / DeepCopy コード生成用
- **Docker**: 20.10+ (オプション)

## クイックスタート

### 1. クローンとビルド

```bash
git clone https://github.com/akam1o/arca-lb.git
cd arca-lb
make deps
make build
```

### 2. CRD のインストール

```bash
make manifests
kubectl apply -f config/crd/bases/
```

### 3. Operator のデプロイ

```bash
kubectl apply -f config/rbac/
kubectl apply -f config/manager/
```

### 4. Agent のデプロイ (DaemonSet)

```bash
kubectl apply -f config/agent/
```

### 5. VirtualIP の作成

```bash
kubectl apply -f config/samples/virtualip_sample.yaml
kubectl get vip
```

## Makefile ターゲット

```bash
make help          # 使用可能なターゲットを表示
make deps          # 依存関係をダウンロード
make build         # Operator と Agent のバイナリをビルド
make test          # テストを実行
make lint          # コード品質チェック
make manifests     # CRD マニフェスト生成 (controller-gen)
make generate      # DeepCopy メソッド生成 (controller-gen)
make proto         # Protocol Buffers コード生成 (v1)
make docker        # Operator と Agent イメージをビルド
make clean         # ビルド成果物を削除
```

## ドキュメント

詳細なドキュメントは `docs/` ディレクトリを参照してください：

### 運用ドキュメント
- [インストールガイド](docs/installation.ja.md) - インストール手順とセットアップ
- [設定ガイド](docs/configuration.ja.md) - Operator と Agent の設定方法
- [API リファレンス](docs/api.ja.md) - CRD API リファレンスと REST API (v1)
- [OpenStack Octavia 連携](docs/octavia.ja.md) - Octavia プロバイダードライバーのセットアップ
- [トラブルシューティング](docs/troubleshooting.ja.md) - よくある問題と解決方法
- [バックエンドサーバー設定ガイド](docs/backend-setup.ja.md) - バックエンドサーバーの設定方法

### 開発者ドキュメント
- [アーキテクチャ](docs/architecture.ja.md) - システムアーキテクチャと設計思想
- [開発環境セットアップ](docs/development.ja.md) - 開発環境のセットアップとワークフロー
- [コントリビューションガイド](docs/contributing.ja.md) - プロジェクトへの貢献方法

## コントリビュート

コントリビューション歓迎です！詳細は [docs/contributing.md](docs/contributing.md) を参照してください。

## お問い合わせ

お問い合わせは [GitHub Issues](https://github.com/akam1o/arca-lb/issues) で Issue を作成してください。セキュリティに関する報告は [GitHub Security Advisories](https://github.com/akam1o/arca-lb/security/advisories) を利用してください。

## ライセンス

Apache License 2.0
