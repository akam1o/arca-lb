# arca-lb インストール手順

このドキュメントでは、arca-lb のインストール手順を説明します。

## 必要要件

### Operator

- **Kubernetes**: 1.28 以上（クラスター）
- **kubectl**: 対象クラスターに設定済み
- **controller-gen**: CRD 生成用（開発時のみ）

### Agent

- **Kubernetes**: 1.28 以上（VirtualIP CRD の監視用）
- **VPP**: 24.10 (推奨、実行時)
- **FRRouting**: 8.0 以上（実行時、BGP 経路広報を使用する場合）

### ビルドツール

- **Go**: 1.24 以上（ソースからビルドする場合）
- **Docker**: 20.10 以上（オプション、コンテナイメージ用）

## インストール方法

### 方法 1: Kubernetes (推奨)

#### 1. バイナリのビルド（またはビルド済みイメージを使用）

```bash
git clone https://github.com/akam1o/arca-lb.git
cd arca-lb
make build
```

#### 2. CRD の生成とインストール

```bash
make manifests
kubectl apply -f config/crd/bases/
```

CRD が登録されたことを確認：

```bash
kubectl get crd virtualips.arca.io
```

#### 3. Operator のデプロイ

```bash
kubectl apply -f config/rbac/
kubectl apply -f config/manager/
```

Operator が稼働していることを確認：

```bash
kubectl get pods -l app=arca-lb-operator
```

#### 4. Agent のデプロイ (DaemonSet)

```bash
kubectl apply -f config/agent/
```

各 LB ノードで Agent が稼働していることを確認：

```bash
kubectl get pods -l app=arca-lb-agent
```

#### 5. VirtualIP の作成

```bash
kubectl apply -f config/samples/virtualip_sample.yaml
```

確認：

```bash
kubectl get vip
```

### 方法 2: Docker イメージのビルド

#### 1. イメージのビルド

```bash
make docker
```

または、個別にビルド：

```bash
docker build -f deploy/docker/Dockerfile.controller -t arcalb-operator:latest .
docker build -f deploy/docker/Dockerfile.agent -t arcalb-agent:latest .
```

#### 2. レジストリにプッシュしてマニフェストを更新

`config/manager/` と `config/agent/` のマニフェストを、ご自身のイメージレジストリを参照するように編集してください。

### 方法 3: Kubernetes 外で Agent を実行

Agent は kubeconfig ファイルを使用して、クラスター外から K8s API Server に接続できます：

```bash
./bin/arcalb-agent --config /path/to/agent.yaml
```

kubeconfig を指定する Agent 設定：

```yaml
kubernetes:
  kubeconfig: "/path/to/kubeconfig"
  namespace: "default"
```

**注意**: VPP ソケットへのアクセスに特権が必要な場合、Agent は `sudo` が必要です。

## 初期設定

### Agent の設定

1. 設定ファイルのコピー

```bash
cp deploy/config/agent.example.yaml deploy/config/agent.yaml
```

2. 設定ファイルの編集

`deploy/config/agent.yaml` を編集して、データプレーン（VPP ソケットパス）、ルーティング（FRR 設定）、Kubernetes 接続設定を指定します。完全なリファレンスは[設定ガイド](./configuration.ja.md)を参照してください。

## 動作確認

### Operator の確認

```bash
kubectl logs -l app=arca-lb-operator --tail=20
```

### Agent の確認

```bash
kubectl logs -l app=arca-lb-agent --tail=20
```

メトリクスが有効な場合：

```bash
# Agent のメトリクスポートにポートフォワード
kubectl port-forward ds/arca-lb-agent 9090:9090
curl http://localhost:9090/metrics
```

### VirtualIP の作成と確認

```bash
# VIP の作成
kubectl apply -f - <<EOF
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: test-vip
spec:
  address: 203.0.113.100
  port: 80
  protocol: TCP
  encapType: L3DSR
  dscp: 10
  backends:
    - address: 10.0.1.1
      weight: 100
EOF

# ステータスの確認
kubectl get vip test-vip -o yaml

# クリーンアップ
kubectl delete vip test-vip
```

## 次のステップ

- [設定ガイド](./configuration.ja.md) を参照して、詳細な設定を確認します
- [API リファレンス](./api.ja.md) を参照して、VirtualIP CRD スキーマを確認します
