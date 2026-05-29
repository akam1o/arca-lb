# arca-lb アーキテクチャ

このドキュメントでは、arca-lb のアーキテクチャと設計思想を説明します。

## 概要

arca-lb は Kubernetes ネイティブなロードバランサー管理システムです。ユーザーは VIP を `VirtualIP` Custom Resource として定義し、Operator がバリデーションとステータス管理を行い、各 LB ノード上の Agent が K8s Informer 経由で CRD を監視して VPP と FRR を制御します。

## システムアーキテクチャ

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
│  - CRD admission バリデーション          │
└──────┬──────────────────────┬───────────┘
       │                      │
       ▼                      ▼
┌──────────────────┐ ┌───────────────────────────┐
│     Operator     │ │   Agent (各 LB ノード)    │
│ ┌──────────────┐ │ │ ┌───────────────────────┐ │
│ │ VirtualIP    │ │ │ │ K8s Watcher           │ │
│ │ Reconciler   │ │ │ │ (Informer + EventHandler)│
│ │ - Validate   │ │ │ └───────────┬───────────┘ │
│ │ - Status     │ │ │             │              │
│ │ - Finalizer  │ │ │             ▼              │
│ └──────────────┘ │ │ ┌───────────────────────┐ │
│ ┌──────────────┐ │ │ │ VIP 別 Reconciler     │ │
│ │ Optional     │ │ │ │ (VIP ごとの goroutine)│ │
│ │ Webhook      │ │ │ └──┬─────────┬──────┬──┘ │
│ └──────────────┘ │ │    │         │      │     │
└──────────────────┘ │    ▼         ▼      ▼     │
                     │ ┌──────┐ ┌──────┐ ┌─────┐ │
                     │ │ Data │ │Router│ │ HC  │ │
                     │ │Plane │ │(FRR) │ │Engine│ │
                     │ └──┬───┘ └──────┘ └─────┘ │
                     │    │                       │
                     │ ┌──┴───────────────┐       │
                     │ │ bbolt ローカルストア│      │
                     │ └──────────────────┘       │
                     │ ┌──────────────────┐       │
                     │ │ OTel + Prometheus│       │
                     │ └──────────────────┘       │
                     └────────────┬───────────────┘
                                  │
                                  ▼
                     ┌────────────────────────────┐
                     │ VPP (Vector Packet Processing) │
                     │ - L4 Load Balancer Plugin       │
                     │ - Maglev Hashing                │
                     └────────────────────────────┘
```

## コンポーネント詳細

### Operator

Operator は Kubernetes クラスター内に Deployment として配置されます：

1. **VirtualIPReconciler**: VirtualIP CRD を監視し、`.status` フィールドを更新（observedGeneration, healthyBackends, conditions）、Finalizer を管理
2. **CRD admission バリデーション**: VirtualIP リソースの作成/更新時にバリデーションを実行（IP フォーマット、ポート範囲、プロトコル、DSCP、バックエンド Weight、ヘルスチェック設定）。CRD スキーマで表現できない検証向けに Webhook 実装を任意で利用できます。

### Agent

Agent は各ロードバランサーノードに DaemonSet として配置されます：

1. **Watcher**: K8s Informer で VirtualIP CRD を監視し、Add/Update/Delete イベントを発火
2. **VIP 別 Reconciler**: VIP ごとに goroutine を生成し、イベント受信時に desired state との差分を計算して DataPlane + Router を同期
3. **DataPlane (インターフェース)**: VPP 制御を抽象化。実装: `VPPDataPlane` (本番), `NoopDataPlane` (テスト)
4. **Router (インターフェース)**: FRR/BGP 経路管理を抽象化。実装: `FRRRouter` (本番), `NoopRouter` (テスト)
5. **HealthCheck Engine**: VIP ごとにプローブ (HTTP/HTTPS, TCP, Ping, TLS hello) を実行し、状態遷移時にコールバックで Reconcile をトリガー
6. **bbolt Store**: ローカルの組み込み KVS で VIP 状態とヘルスチェック結果をキャッシュ。K8s API 接続断時のフォールバックを提供
7. **Metrics / Telemetry**: Prometheus エンドポイント + OpenTelemetry (OTLP) によるトレースとメトリクス

## データフロー

### VIP 作成フロー

```
1. User → Kubernetes API
   kubectl apply -f virtualip.yaml

2. API Server → CRD スキーマバリデーション
   VirtualIP spec のバリデーション

3. API Server → etcd
   VirtualIP リソースの保存

4. Informer (Agent) → Watcher
   Add イベントの受信

5. Watcher → VIP 別 Reconciler
   OnVIPUpdate(vip)

6. Reconciler → DataPlane (VPP)
   EnsureVIP() + SyncBackends()

7. Reconciler → Router (FRR)
   AnnounceRoute()

8. Watcher → HealthCheck Engine
   UpdateVIP() → プローブ開始
```

### ヘルスチェックフロー

```
1. HealthCheck Engine → Prober
   設定された間隔で Probe() を実行

2. Prober → Backend Server
   HTTP/TCP/Ping リクエスト

3. Prober → HealthCheck Engine
   V2ProbeResult (成功/失敗)

4. HealthCheck Engine → State Tracker
   rise/fall カウンター更新

5. HealthCheck Engine → Reconciler (コールバック)
   OnHealthChange(vipName)

6. Reconciler → DataPlane (VPP)
   SyncBackends() (不健全なバックエンドを追加/除外)

7. Reconciler → Router (FRR)
   健全なバックエンドがない場合 WithdrawRoute()
```

### VIP 削除フロー

```
1. User → Kubernetes API
   kubectl delete virtualip web-vip

2. Informer (Agent) → Watcher
   Delete イベントの受信

3. Watcher → HealthCheck Engine
   StopVIP()

4. Watcher → VIP 別 Reconciler
   OnVIPDelete(vip)

5. Reconciler → DataPlane (VPP)
   DeleteVIP()

6. Reconciler → Router (FRR)
   WithdrawRoute()
```

## 設計原則

### 1. Kubernetes ネイティブ

- VirtualIP CRD が単一の真実の源（Single Source of Truth）として機能
- ユーザーは `kubectl` や GitOps で宣言的に VIP を管理
- 独自の REST API やデータストアは不要

### 2. 宣言的設定

- ユーザーは VirtualIP リソースで「どのような状態にしたいか」を指定
- Agent が「現在の状態」と「望ましい状態」の差分を検出して同期

### 3. イベント駆動

- K8s Informer が設定変更をリアルタイムに Agent に配信
- ヘルスチェックの状態変化が即座に Reconcile をトリガー

### 4. 障害耐性

- Agent は K8s API が利用不可でも、ローカルの bbolt ストアを使用して動作を継続
- VPP 設定は Agent 停止後も維持（Graceful Shutdown）

### 5. プラガブルインターフェース

- DataPlane と Router は Go interface として定義され、テストダブルや代替バックエンドの差し替えが可能
- Noop 実装により、VPP/FRR なしでの開発・テストが容易

### 6. 可観測性

- OpenTelemetry によるトレースとメトリクス（OTLP エクスポート）
- Prometheus メトリクスエンドポイントによる監視
- 構造化ログ (`log/slog`) によるデバッグ支援

## 技術スタック

### Operator

- **言語**: Go 1.25 モジュール言語バージョン、ビルドとテストは Go toolchain 1.26.3
- **フレームワーク**: controller-runtime (sigs.k8s.io/controller-runtime)
- **バリデーション**: CRD OpenAPI/CEL バリデーションと任意の Admission Webhook 実装

### Agent

- **言語**: Go 1.25 モジュール言語バージョン、ビルドとテストは Go toolchain 1.26.3
- **K8s 連携**: client-go Informers
- **VPP 連携**: go.fd.io/govpp v0.13.0
- **FRR 連携**: vtysh コマンド経由
- **ローカルストア**: go.etcd.io/bbolt
- **メトリクス**: Prometheus client_golang + OpenTelemetry

## スケーラビリティ

### 水平スケーリング

- Operator: Leader Election 付きの単一インスタンス（または複数レプリカ）
- Agent: 各 LB ノードに 1 インスタンス（DaemonSet として配置）

### パフォーマンス

- VPP による高速パケット処理（ユーザースペース）
- VIP 別の Reconciler goroutine による並列処理
- Worker Pool パターンによる非同期ヘルスチェック（並列実行）
- 効率的なリコンシリエーション（差分のみ更新）

## セキュリティ

### 現在の実装

- K8s RBAC による VirtualIP CRD へのアクセス制御
- CRD OpenAPI/CEL バリデーションにより、不正な VirtualIP spec を admission 時に拒否
- Agent は最小権限の RBAC（VirtualIP リソースの読み取り専用）

### 推奨事項

- Agent ↔ K8s API 間の通信は TLS で暗号化
- NetworkPolicy で Agent のトラフィックを制限
- VPP は適切なセキュリティプロファイルで実行

## 次のステップ

- [開発環境セットアップ](./development.ja.md) を参照して、開発を開始します
- [コントリビューションガイド](./contributing.ja.md) を参照して、プロジェクトに貢献します
