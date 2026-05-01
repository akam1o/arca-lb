# OpenStack Octavia 連携

本ドキュメントでは、arca-lb を OpenStack Octavia のプロバイダードライバーとして統合する方法を説明します。

## 概要

arca-lb は Octavia プロバイダードライバーを提供し、OpenStack のロードバランサー API 操作を Kubernetes クラスター上の VirtualIP カスタムリソース操作に変換します。これにより、OpenStack ユーザーは標準の Octavia API を通じて、高性能な VPP ベースの L3DSR ロードバランサーをプロビジョニングできます。

### アーキテクチャ

```
OpenStack テナント (API / Horizon / CLI)
         │
         ▼
┌─────────────────────────────┐
│   Octavia API               │
│   ├─ amphora driver         │
│   └─ arca driver  ◄────────│── octavia-arca-driver (Python)
└─────────┬───────────────────┘
          │ Kubernetes API (VirtualIP CRD)
          ▼
┌─────────────────────────────┐
│   Kubernetes API Server     │
│   VirtualIP CRD             │
└──────┬──────────────┬───────┘
       │              │
       ▼              ▼
   Operator        Agent (DaemonSet)
   (ステータス)     (VPP + FRR)
```

### コンセプトマッピング

| Octavia | arca-lb VirtualIP | 備考 |
|---------|-------------------|------|
| Loadbalancer | VIP アドレス | 1 つの LB に複数の VirtualIP（Listener ごとに 1 つ） |
| Listener | VirtualIP リソース | port + protocol が VirtualIP を定義 |
| Pool | backends[] | バックエンドリストにマッピング |
| Member | backends[].address + monitorAddress + weight | 個別のバックエンドエントリ。`monitor_address` はヘルスチェック用の `monitorAddress` にマッピングされます。 |
| HealthMonitor | healthCheck | HTTP、HTTPS、TCP、PING、TLS-HELLO に対応します。UDP-CONNECT は拒否します。 |
| L7Policy/Rule | *（非対応）* | arca-lb は L4 ロードバランサー |

Member weight は VirtualIP backend spec に保持されますが、現時点ではデータプレーン上の実トラフィック分散には反映されません。現在の VPP LB plugin 経路では metadata のみとして扱われます。VPP LB API が backend weight を公開した時点で重み付き AS programming に反映されます。

## 前提条件

- Octavia がデプロイされた OpenStack（Zed 以降推奨）
- arca-lb の Operator と Agent が稼働する Kubernetes クラスター
- Octavia API ホストと Kubernetes API サーバー間のネットワーク接続
- Python 3.9+

## インストール

### 1. ドライバーパッケージのインストール

```bash
cd octavia-driver/
pip install .
```

または、リポジトリから直接インストール：

```bash
pip install git+https://github.com/akam1o/arca-lb.git#subdirectory=octavia-driver
```

### 2. Octavia の設定

`/etc/octavia/octavia.conf` に arca ドライバーを追加します：

```ini
[api_settings]
enabled_provider_drivers = amphora:The Octavia Amphora driver., arca:ArcaLB VPP-based L3DSR LB driver.
default_provider_driver = amphora

[driver_arca]
# kubeconfig ファイルのパス。空の場合はクラスター内設定を使用。
kubernetes_config = /etc/octavia/kubeconfig

# VirtualIP リソースが作成される Kubernetes 名前空間。
namespace = arca-system

# デフォルトのカプセル化タイプ (GRE4, GRE6, L3DSR, NAT4, NAT6)。
default_encap_type = L3DSR

# DSCP ベース L3DSR モードのデフォルト DSCP 値 (1-63)。
default_dscp = 10

# VirtualIP ステータスを Octavia に同期する間隔（秒）。
status_sync_interval = 10
```

### 3. Kubernetes アクセスの設定

適切な RBAC 権限を持つ kubeconfig を作成します：

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: octavia-arca-driver
  namespace: arca-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: octavia-arca-driver
  namespace: arca-system
rules:
  - apiGroups: ["arca.io"]
    resources: ["virtualips"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: octavia-arca-driver
  namespace: arca-system
subjects:
  - kind: ServiceAccount
    name: octavia-arca-driver
    namespace: arca-system
roleRef:
  kind: Role
  name: octavia-arca-driver
  apiGroup: rbac.authorization.k8s.io
```

```bash
kubectl apply -f octavia-rbac.yaml
```

### 4. Octavia API の再起動

```bash
systemctl restart octavia-api
```

### 5. ドライバーの確認

```bash
openstack loadbalancer provider list
```

期待される出力：

```
+---------+---------------------------------------------+
| name    | description                                 |
+---------+---------------------------------------------+
| amphora | The Octavia Amphora driver.                 |
| arca    | ArcaLB VPP-based L3DSR LB driver.           |
+---------+---------------------------------------------+
```

## 使用方法

### ロードバランサーの作成

```bash
# arca プロバイダーでロードバランサーを作成
openstack loadbalancer create \
  --name web-lb \
  --provider arca \
  --vip-address 203.0.113.10 \
  --vip-subnet-id <subnet-id>

# リスナーの作成
openstack loadbalancer listener create \
  --name web-listener \
  --protocol TCP \
  --protocol-port 80 \
  web-lb

# プールの作成
openstack loadbalancer pool create \
  --name web-pool \
  --protocol TCP \
  --lb-algorithm SOURCE_IP \
  --listener web-listener

# メンバーの追加
openstack loadbalancer member create \
  --name backend-1 \
  --address 10.0.1.1 \
  --protocol-port 80 \
  --weight 100 \
  web-pool

openstack loadbalancer member create \
  --name backend-2 \
  --address 10.0.1.2 \
  --protocol-port 80 \
  --weight 100 \
  web-pool

# ヘルスモニターの作成
openstack loadbalancer healthmonitor create \
  --name web-hm \
  --type HTTP \
  --delay 10 \
  --timeout 5 \
  --max-retries 3 \
  --max-retries-down 2 \
  --http-method GET \
  --url-path /healthz \
  --expected-codes 200 \
  web-pool
```

### VirtualIP の確認

```bash
kubectl get virtualips -n arca-system
```

期待される出力：

```
NAME                       ADDRESS        PORT   PROTOCOL   HEALTHY   TOTAL   AGE
octavia-xxxxxxxx-yyyyyyyy  203.0.113.10   80     TCP        2         2       30s
```

### フレーバーによるカプセル化設定

Octavia フレーバーを作成してカプセル化タイプをカスタマイズできます：

```bash
# フレーバープロファイルの作成
openstack loadbalancer flavorprofile create \
  --name arca-gre4 \
  --provider arca \
  --flavor-data '{"encap_type": "GRE4"}'

# フレーバーの作成
openstack loadbalancer flavor create \
  --name gre4-lb \
  --flavorprofile arca-gre4

# フレーバーを使用してロードバランサーを作成
openstack loadbalancer create \
  --name gre-lb \
  --provider arca \
  --flavor gre4-lb \
  --vip-address 203.0.113.20 \
  --vip-subnet-id <subnet-id>
```

利用可能なフレーバーメタデータ：

| キー | 値 | デフォルト | 説明 |
|------|------|-----------|------|
| `encap_type` | GRE4, GRE6, L3DSR, NAT4, NAT6 | L3DSR | 戻りトラフィックのカプセル化タイプ |
| `dscp` | 1-63 | 10 | DSCP ベース L3DSR 用の DSCP マーキング値 |

## 制限事項

- **L4 のみ**: L7 ポリシーとルールは非対応です。arca-lb はレイヤー 4 ロードバランサーです。
- **負荷分散アルゴリズム**: VPP は内部的に Maglev コンシステントハッシュを使用します。`lb_algorithm` パラメータは受け付けますが、基盤のアルゴリズムは常に Maglev です（`SOURCE_IP` と機能的に類似）。
- **Member weight**: Octavia member の `weight` は VirtualIP backend spec に保存されますが、現時点では実トラフィックの分散比率には影響しません。VPP LB API が backend weight を公開した時点でデータプレーンに接続します。
- **フェイルオーバー**: 手動フェイルオーバーは非対応です。arca-lb は BGP ECMP による自動フェイルオーバーに依存します。
- **Floating IP**: VIP アドレスは arca-lb の BGP アナウンスメントで管理され、Neutron の Floating IP ではありません。
- **TERMINATED_HTTPS**: LB レベルでの TLS 終端は非対応です。バックエンド側の TLS による TCP パススルーを使用してください。
- **UDP-CONNECT health monitor**: 非対応です。TCP probe に暗黙変換せず、driver が拒否します。

## トラブルシューティング

### ドライバーがプロバイダーリストに表示されない

1. パッケージがインストールされているか確認: `pip show octavia-arca-driver`
2. エントリーポイントを確認: `pip show -f octavia-arca-driver | grep entry`
3. `octavia.conf` の `enabled_provider_drivers` に `arca` が含まれているか確認
4. Octavia API ログを確認: `journalctl -u octavia-api`

### VirtualIP が作成されない

1. Octavia API ログで arca ドライバーのエラーを確認
2. Kubernetes 接続を確認: `kubectl --kubeconfig /etc/octavia/kubeconfig get virtualips -n arca-system`
3. サービスアカウントの RBAC 権限を確認

### VirtualIP が作成されたがプログラムされない

1. arca-lb Agent ログを確認: `kubectl logs -n arca-system -l app.kubernetes.io/name=arca-lb-agent`
2. CRD ステータスを確認: `kubectl describe virtualip -n arca-system <name>`
3. VPP ステータスを確認: `vppctl show lb vip verbose`

### ステータスが Octavia に同期されない

1. `octavia.conf` の `status_sync_interval` を確認
2. VirtualIP に `Ready` Condition が設定されているか確認
3. ドライバーログでステータス更新エラーを確認
