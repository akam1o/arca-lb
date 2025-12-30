# arca-lb インストール手順

このドキュメントでは、arca-lb のインストール手順を説明します。

## 必要要件

### Controller

- **Go**: 1.23 以上（ビルド時）
- **MySQL**: 8.0 以上（オプション、etcd を使用する場合は不要）
- **etcd**: 3.5 以上（オプション、MySQL を使用する場合は不要）
- **Docker**: 20.10 以上（オプション、コンテナ実行時）

### Agent

- **Go**: 1.23 以上（ビルド時）
- **VPP**: 22.02 以上（実行時）
- **FRRouting**: 8.0 以上（実行時、BGP 経路広報を使用する場合）
- **Docker**: 20.10 以上（オプション、コンテナ実行時）

## インストール方法

### 方法 1: バイナリからインストール

#### 1. リポジトリのクローン

```bash
git clone https://github.com/akam1o/arca-lb.git
cd arca-lb
```

#### 2. 依存関係のインストール

```bash
make deps
```

#### 3. ビルド

```bash
make build
```

ビルドが成功すると、`bin/` ディレクトリに以下のバイナリが生成されます：

- `bin/arcalb-controller` - Controller バイナリ
- `bin/arcalb-agent` - Agent バイナリ

### 方法 2: Docker イメージからインストール

#### 1. Docker イメージのビルド

```bash
make docker
```

または、個別にビルド：

```bash
docker build -f deploy/docker/Dockerfile.controller -t arcalb-controller:latest .
docker build -f deploy/docker/Dockerfile.agent -t arcalb-agent:latest .
```

#### 2. Docker Compose を使用した起動

```bash
cd deploy/docker-compose
docker compose -f docker-compose.dev.yml up -d
```

### 方法 3: Kubernetes にデプロイ

#### 1. 名前空間の作成

```bash
kubectl create namespace arca-lb
```

#### 2. Controller のデプロイ

```bash
kubectl apply -f deploy/kubernetes/controller-deployment.yaml
```

#### 3. Agent のデプロイ

```bash
kubectl apply -f deploy/kubernetes/agent-daemonset.yaml
```

#### 4. vpp-exporter のデプロイ（オプション）

```bash
kubectl apply -f deploy/kubernetes/vpp-exporter-daemonset.yaml
```

## 初期設定

### Controller の設定

1. 設定ファイルのコピー

```bash
cp deploy/config/controller.example.yaml deploy/config/controller.yaml
```

2. 設定ファイルの編集

`deploy/config/controller.yaml` を編集して、データストア（MySQL または etcd）の接続情報を設定します。

### Agent の設定

1. 設定ファイルのコピー

```bash
cp deploy/config/agent.example.yaml deploy/config/agent.yaml
```

2. 設定ファイルの編集

`deploy/config/agent.yaml` を編集して、Controller の gRPC エンドポイントと VPP の設定を指定します。

## 起動

### Controller の起動

```bash
./bin/arcalb-controller --config deploy/config/controller.yaml
```

または、Docker を使用：

```bash
docker run -d \
  --name arcalb-controller \
  -v $(pwd)/deploy/config/controller.yaml:/app/config/controller.yaml:ro \
  -p 8080:8080 \
  -p 50051:50051 \
  arcalb-controller:latest
```

### Agent の起動

```bash
# 環境変数で設定ファイルのパスを指定
export ARCA_AGENT_CONFIG=deploy/config/agent.yaml
sudo ./bin/arcalb-agent
```

**注意**: Agent は VPP のソケットにアクセスするため、`sudo` が必要な場合があります。また、Agent は `--config` フラグではなく、`ARCA_AGENT_CONFIG` 環境変数で設定ファイルのパスを指定します。

または、Docker を使用（ホストネットワークモード）：

```bash
docker run -d \
  --name arcalb-agent \
  --privileged \
  --network host \
  -v /run/vpp/api.sock:/run/vpp/api.sock:ro \
  -v /run/vpp/stats.sock:/run/vpp/stats.sock:ro \
  -v $(pwd)/deploy/config/agent.yaml:/app/config/agent.yaml:ro \
  arcalb-agent:latest
```

## 動作確認

### Controller の動作確認

```bash
curl http://localhost:8080/healthz
```

正常な場合、以下のようなレスポンスが返ります：

```json
{
  "status": "healthy",
  "time": "2025-12-20T10:00:00Z"
}
```

### Agent の動作確認

Agent のログを確認して、正常に起動しているか確認します：

```bash
# ログにエラーがないか確認
# メトリクスが有効な場合（metrics.enabled: true）
curl http://localhost:9090/metrics
```

**注意**: メトリクスはデフォルトで無効（`metrics.enabled: false`）です。メトリクスを有効にするには、設定ファイルで `metrics.enabled: true` を設定してください。

## 次のステップ

- [設定ガイド](./configuration.ja.md) を参照して、詳細な設定を行います
- [REST API リファレンス](./api.ja.md) を参照して、API の使用方法を確認します
