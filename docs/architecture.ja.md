# arca-lb アーキテクチャ

このドキュメントでは、arca-lb のアーキテクチャと設計思想について説明します。

## 概要

arca-lb は、中央集約型のロードバランサー管理システムです。Controller が中央で VIP とバックエンドを管理し、Agent が各ノードで VPP を制御して実際のロードバランシングを実行します。

## システムアーキテクチャ

```
┌─────────────────────────────────────────┐
│         REST API Client                  │
│  (kubectl, curl, 管理ツール)              │
└────────────┬────────────────────────────┘
             │ HTTP/REST
             ▼
┌─────────────────────────────────────────┐
│      Controller                         │
│  ┌──────────────────────────────────┐  │
│  │  REST API Server (Gin)            │  │
│  │  - VIP/Backend CRUD               │  │
│  │  - 設定管理                        │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  gRPC Server                      │  │
│  │  - Agent への設定配信              │  │
│  │  - Agent 登録管理                 │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  DataStore (etcd/MySQL)           │  │
│  │  - VIP/Backend 永続化              │  │
│  │  - リビジョン管理                  │  │
│  └──────────────────────────────────┘  │
└────────────┬────────────────────────────┘
             │ gRPC
             ▼
┌─────────────────────────────────────────┐
│      Agent (各ロードバランサーノード)     │
│  ┌──────────────────────────────────┐  │
│  │  gRPC Client                      │  │
│  │  - Controller からの設定受信      │  │
│  │  - ハートビート送信                │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  State Manager                   │  │
│  │  - 現在の設定状態保持              │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  Reconciler                      │  │
│  │  - 設定差分検出                   │  │
│  │  - 各コンポーネントへの同期        │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  VPP Syncer                      │  │
│  │  - VPP LB プラグイン制御          │  │
│  │  - VIP/Backend 設定               │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  Health Check Manager            │  │
│  │  - バックエンドヘルスチェック      │  │
│  │  - 状態管理                       │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  FRR Manager                     │  │
│  │  - BGP 経路広報制御               │  │
│  └──────────────────────────────────┘  │
│  ┌──────────────────────────────────┐  │
│  │  Metrics Server                  │  │
│  │  - Prometheus メトリクス公開       │  │
│  └──────────────────────────────────┘  │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│      VPP (Vector Packet Processing)      │
│  - L4 Load Balancer Plugin               │
│  - 高速パケット処理                       │
└─────────────────────────────────────────┘
```

## コンポーネント詳細

### Controller

Controller は中央管理コンポーネントで、以下の責務を持ちます：

1. **REST API Server**: VIP とバックエンドの CRUD 操作を提供
2. **gRPC Server**: Agent への設定配信と Agent 登録管理
3. **DataStore**: VIP とバックエンドの永続化（etcd または MySQL）

### Agent

Agent は各ロードバランサーノードで実行されるコンポーネントで、以下の責務を持ちます：

1. **gRPC Client**: Controller からの設定を受信
2. **State Manager**: 現在の設定状態を保持
3. **Reconciler**: 設定差分を検出し、各コンポーネントに同期
4. **VPP Syncer**: VPP の LB プラグインを制御
5. **Health Check Manager**: バックエンドのヘルスチェックを実行
6. **FRR Manager**: BGP 経路広報を制御
7. **Metrics Server**: Prometheus メトリクスを公開

## データフロー

### VIP 作成フロー

```
1. REST API Client → Controller REST API
   POST /api/v1/vips

2. Controller → DataStore
   CreateVIP()

3. Controller → Agent (gRPC)
   ConfigSync.GetConfig() または WatchConfig()

4. Agent → State Manager
   UpdateConfig()

5. Agent → Reconciler
   TriggerReconcile()

6. Agent → VPP Syncer
   SyncVIP()

7. Agent → FRR Manager (オプション)
   AnnounceRoute()

8. Agent → Health Check Manager
   StartHealthCheck()
```

### ヘルスチェックフロー

```
1. Health Check Manager → Prober
   Probe()

2. Prober → Backend Server
   HTTP/TCP/Ping リクエスト

3. Prober → Health Check Manager
   ProbeResult

4. Health Check Manager → State Tracker
   UpdateState()

5. Health Check Manager → VPP Syncer (必要に応じて)
   UpdateBackendState()
```

## 設計原則

### 1. 中央集約管理

- Controller が単一の真実の源（Single Source of Truth）として機能
- Agent は Controller からの設定を受動的に受信

### 2. 宣言的設定

- ユーザーは「どのような状態にしたいか」を指定
- Agent が「現在の状態」と「望ましい状態」の差分を検出して同期

### 3. イベント駆動

- 設定変更は gRPC ストリームでリアルタイムに配信
- Agent は設定変更を即座に反映

### 4. 障害耐性

- Agent は Controller との接続が切れても動作を継続
- VPP 設定は Agent 停止後も維持（Graceful Shutdown）

### 5. 可観測性

- Prometheus メトリクスによる監視
- 構造化ログによるデバッグ支援

## 技術スタック

### Controller

- **言語**: Go 1.23
- **Web フレームワーク**: Gin
- **gRPC**: google.golang.org/grpc
- **データストア**: etcd (推奨) または MySQL

### Agent

- **言語**: Go 1.23
- **VPP 連携**: go.fd.io/govpp v0.13.0
- **FRR 連携**: vtysh コマンド経由
- **メトリクス**: Prometheus client_golang

## スケーラビリティ

### 水平スケーリング

- Controller: 複数インスタンスで実行可能（データストアを共有）
- Agent: 各ノードに 1 インスタンス（DaemonSet として配置）

### パフォーマンス

- VPP による高速パケット処理（ユーザースペース）
- 非同期ヘルスチェック（並列実行）
- 効率的なリコンシリエーション（差分のみ更新）

## セキュリティ

### 現在の実装

- 認証・認可は未実装（本番環境では実装が必要）
- TLS はオプション（gRPC でサポート）

### 推奨事項

- Controller と Agent 間の通信は TLS で暗号化
- REST API は認証・認可を実装
- データストアへのアクセスは適切に制限

## 次のステップ

- [開発環境セットアップ](./development.ja.md) を参照して、開発を開始します
- [コントリビューションガイド](./contributing.ja.md) を参照して、プロジェクトに貢献します
