# arca-lb 開発環境セットアップ

このドキュメントでは、arca-lb の開発環境のセットアップ方法を説明します。

## 必要要件

### 必須

- **Go**: 1.24 以上
- **Git**: 2.0 以上
- **Make**: 3.0 以上
- **Kubernetes**: 1.28 以上（統合テスト用）
- **kubectl**: 開発クラスターに設定済み

### オプション

- **golangci-lint**: コード品質チェック用
- **controller-gen**: CRD および DeepCopy コード生成用
- **protoc**: Protocol Buffers コンパイラ（v1 gRPC コード生成用）
- **Docker**: 20.10 以上（コンテナイメージ用）
- **kind**: テスト用ローカル K8s クラスター

## セットアップ手順

### 1. リポジトリのクローン

```bash
git clone https://github.com/akam1o/arca-lb.git
cd arca-lb
```

### 2. 依存関係のインストール

```bash
make deps
```

### 3. 開発ツールのインストール

#### controller-gen（CRD 開発に必須）

```bash
make install-controller-gen
# または
go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest
```

#### golangci-lint

```bash
# macOS
brew install golangci-lint

# Linux
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.55.2
```

#### protoc（v1 のみ）

```bash
# macOS
brew install protobuf

# Linux (Ubuntu/Debian)
sudo apt-get install protobuf-compiler

# プラグインのインストール
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

### 4. 開発用 Kubernetes クラスターのセットアップ（オプション）

```bash
# kind を使用
kind create cluster --name arca-lb-dev

# CRD のインストール
make manifests
kubectl apply -f config/crd/bases/
```

## 開発ワークフロー

### 1. コードの変更

```bash
# ブランチの作成
git checkout -b feature/my-feature

# コードの編集
# ...

# コードのフォーマット
make fmt

# コードの品質チェック
make lint

# テストの実行
make test
```

### 2. CRD 型の変更

`api/v1alpha1/types.go` を変更した場合：

```bash
# CRD マニフェストの再生成
make manifests

# DeepCopy メソッドの再生成
make generate

# 開発クラスターに CRD を再適用
kubectl apply -f config/crd/bases/
```

### 3. Protocol Buffers の変更（v1 のみ）

```bash
# proto ファイルを編集
# api/proto/*.proto

# コード生成
make proto
```

### 4. ビルドとテスト

```bash
# v2 のみビルド
make build-v2

# すべてビルド (v1 + v2)
make build

# テスト
make test

# カバレッジレポート
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 5. ローカル実行

```bash
# Operator の実行（現在の kubeconfig コンテキストに接続）
./bin/arcalb-operator --metrics-bind-address=:8080

# Agent の実行（テスト用 noop データプレーン）
./bin/arcalb-agent-v2 --config deploy/config/agent.yaml
```

## デバッグ

### Operator のデバッグ

```bash
# 詳細ログで実行
./bin/arcalb-operator --metrics-bind-address=:8080
# controller-runtime はデフォルトで dev モードの zap ロガーを使用
```

### Agent のデバッグ

```bash
# Agent 設定ファイルで log.level: "debug" を設定
./bin/arcalb-agent-v2 --config deploy/config/agent.yaml
```

VPP/FRR なしで開発する場合は `noop` データプレーンとルーターを使用：

```yaml
dataplane:
  type: "noop"
routing:
  enabled: false
  type: "noop"
```

### VPP のデバッグ

```bash
# VPP CLI に接続
sudo vppctl

# VIP の確認
show lb vip

# バックエンドの確認
show lb as
```

### VirtualIP リソースの確認

```bash
# すべての VIP を一覧表示
kubectl get vip -o wide

# 詳細なステータスを表示
kubectl get vip web-vip -o yaml

# 変更を監視
kubectl get vip -w
```

## コードスタイル

### Go ガイドライン

- [Effective Go](https://go.dev/doc/effective_go) に従う
- `gofmt` でフォーマット
- `golangci-lint` で品質チェック

### 命名規則

- **パッケージ**: 小文字、単数形
- **型**: PascalCase
- **関数**: PascalCase（エクスポート）、camelCase（非エクスポート）
- **定数**: PascalCase（エクスポート）、camelCase（非エクスポート）

### エラーハンドリング

```go
// エラーを明示的に処理
if err != nil {
    return fmt.Errorf("context: %w", err)
}
```

### ログ

```go
// log/slog で構造化ログ (v2)
slog.Info("VIP reconciled",
    "vip", vipName,
    "backends", len(backends),
)
```

## テスト

### ユニットテスト

```bash
# すべてのテストを実行
make test

# 特定のパッケージのテスト
go test ./internal/agent/reconciler/...

# レースディテクター
go test -race ./...
```

### 統合テスト

```bash
# 統合テストの実行
go test -tags=integration ./test/integration/...
```

### カバレッジ

```bash
# カバレッジレポートの生成
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 主な Makefile ターゲット

```bash
make help          # すべてのターゲットを表示
make build-v2      # v2 Operator + Agent をビルド
make test          # レースディテクター付きテスト実行
make lint          # golangci-lint を実行
make manifests     # CRD マニフェスト生成
make generate      # DeepCopy メソッド生成
make fmt           # コードフォーマット
make vet           # go vet を実行
make clean         # ビルド成果物を削除
```

## リリース

### バージョンタグ

```bash
git tag -a v2.0.0 -m "Release v2.0.0"
git push origin v2.0.0
```

### Docker イメージのビルド

```bash
make docker
```

## トラブルシューティング

### ビルドエラー

```bash
# 依存関係の更新
go mod tidy
go mod download
```

### テストエラー

```bash
# テストキャッシュのクリア
go clean -testcache
go test ./...
```

### CRD が更新されない

```bash
# 再生成して再適用
make manifests
kubectl apply -f config/crd/bases/
```

## 次のステップ

- [アーキテクチャ](./architecture.ja.md) を参照して、システム設計を理解します
- [コントリビューションガイド](./contributing.ja.md) を参照して、プロジェクトに貢献します
