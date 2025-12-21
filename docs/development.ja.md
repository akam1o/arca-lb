# arca-lb 開発環境セットアップ

このドキュメントでは、arca-lb の開発環境のセットアップ方法を説明します。

## 必要要件

### 必須

- **Go**: 1.23 以上
- **Git**: 2.0 以上
- **Make**: 3.0 以上
- **Docker**: 20.10 以上（オプション、統合テスト用）
- **Docker Compose**: 2.0 以上（オプション、統合テスト用）

### オプション

- **golangci-lint**: コード品質チェック用
- **protoc**: Protocol Buffers コンパイラ（gRPC コード生成用）
- **etcd**: データストア（開発用）

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

#### golangci-lint

```bash
# macOS
brew install golangci-lint

# Linux
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.55.2
```

#### protoc

```bash
# macOS
brew install protobuf

# Linux (Ubuntu/Debian)
sudo apt-get install protobuf-compiler

# プラグインのインストール
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

### 4. 開発環境の起動

#### etcd の起動（Docker Compose）

```bash
cd deploy/docker-compose
docker compose -f docker-compose.dev.yml up -d etcd
```

#### 設定ファイルの準備

```bash
cp deploy/config/controller.example.yaml deploy/config/controller.yaml
cp deploy/config/agent.example.yaml deploy/config/agent.yaml
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

### 2. Protocol Buffers の変更

```bash
# proto ファイルを編集
# api/proto/*.proto

# コード生成
make proto
```

### 3. ビルドとテスト

```bash
# ビルド
make build

# テスト
make test

# カバレッジレポート
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 4. 統合テストの実行

```bash
# 統合テストの実行（etcd が必要）
go test -tags=integration ./test/integration/...
```

## デバッグ

### Controller のデバッグ

```bash
# デバッグログレベルで起動
./bin/arcalb-controller --config deploy/config/controller.yaml
# 設定ファイルで log.level: "debug" を設定
```

### Agent のデバッグ

```bash
# デバッグログレベルで起動
export ARCA_AGENT_CONFIG=deploy/config/agent.yaml
sudo ./bin/arcalb-agent
# 設定ファイルで log.level: "debug" を設定
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

## コードスタイル

### Go コーディング規約

- [Effective Go](https://go.dev/doc/effective_go) に従う
- `gofmt` でフォーマット
- `golangci-lint` で品質チェック

### 命名規則

- **パッケージ名**: 小文字、単数形
- **型名**: 大文字始まり、PascalCase
- **関数名**: 大文字始まり（公開）、小文字始まり（非公開）
- **定数**: 大文字、UPPER_SNAKE_CASE

### エラーハンドリング

```go
// エラーは明示的に処理
if err != nil {
    return fmt.Errorf("context: %w", err)
}
```

### ログ

```go
// 構造化ログを使用
logger.WithFields(logrus.Fields{
    "vip_id": vipID,
    "error": err,
}).Error("Failed to create VIP")
```

## テスト

### ユニットテスト

```bash
# すべてのテストを実行
make test

# 特定のパッケージのテスト
go test ./internal/controller/api/...

# レース検出付きテスト
go test -race ./...
```

### 統合テスト

```bash
# 統合テストを実行
go test -tags=integration ./test/integration/...

# 短いテストをスキップ
go test -tags=integration -short=false ./test/integration/...
```

### テストカバレッジ

```bash
# カバレッジレポートの生成
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## リリース

### バージョンタグ

```bash
# バージョンタグの作成
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

### Docker イメージのビルド

```bash
# Docker イメージのビルド
make docker

# 特定のタグでビルド
docker build -f deploy/docker/Dockerfile.controller -t arcalb-controller:v1.0.0 .
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

### リンターエラー

```bash
# リンターの実行
make lint

# 自動修正可能な問題を修正
golangci-lint run --fix
```

## 次のステップ

- [アーキテクチャ詳細](./architecture.md) を参照して、システムの設計を理解します
- [コントリビューションガイド](./contributing.md) を参照して、プロジェクトに貢献します

