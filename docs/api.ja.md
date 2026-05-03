# arca-lb API リファレンス

このドキュメントは arca-lb の API リファレンスです。

## VirtualIP CRD API (v2)

### リソース概要

| フィールド | 値 |
|----------|-----|
| API Group | `arca.io` |
| API Version | `v1alpha1` |
| Kind | `VirtualIP` |
| Short Name | `vip` |
| Scope | Namespaced |

### kubectl 使用例

```bash
# VIP の一覧表示
kubectl get vip
kubectl get vip -o wide

# VIP の作成
kubectl apply -f virtualip.yaml

# VIP ステータスの表示
kubectl get vip web-vip -o yaml

# VIP の削除
kubectl delete vip web-vip

# 変更の監視
kubectl get vip -w
```

### VirtualIP リソース

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: web-vip
  namespace: default
spec:
  address: "203.0.113.10"
  port: 80
  protocol: TCP
  encapType: L3DSR
  dscp: 10
  backends:
    - address: "10.0.1.1"
      weight: 1
    - address: "10.0.1.2"
      weight: 1
  healthCheck:
    type: http
    intervalSeconds: 5
    timeoutSeconds: 3
    riseCount: 3
    fallCount: 2
    http:
      port: 8080
      path: /healthz
      method: GET
      expectedCodes: [200]
status:
  observedGeneration: 5
  healthyBackends: 2
  totalBackends: 2
  backends:
    - address: "10.0.1.1"
      healthy: true
      lastProbeTime: "2025-01-15T10:00:00Z"
    - address: "10.0.1.2"
      healthy: true
      lastProbeTime: "2025-01-15T10:00:00Z"
  conditions:
    - type: Ready
      status: "True"
      lastTransitionTime: "2025-01-15T09:00:00Z"
```

### Spec フィールド

#### VirtualIPSpec

| フィールド | 型 | 必須 | 説明 |
|----------|-----|------|------|
| `address` | string (IP) | はい | 仮想 IP アドレス |
| `port` | int (1-65535) | はい | 仮想ポート番号 |
| `protocol` | string | はい | トランスポートプロトコル: `TCP` または `UDP` |
| `encapType` | string | いいえ | カプセル化タイプ: `GRE4`, `GRE6`, `L3DSR`, `NAT4`, `NAT6`。デフォルト: `L3DSR` |
| `dscp` | int (1-63) | いいえ | DSCP ベース L3DSR 用の任意 override。省略時は Agent の既定値を使用 |
| `backends` | []BackendSpec | いいえ | バックエンドサーバーのリスト |
| `healthCheck` | HealthCheckSpec | いいえ | ヘルスチェック設定 |

#### BackendSpec

| フィールド | 型 | 必須 | 説明 |
|----------|-----|------|------|
| `address` | string (IP) | はい | バックエンドサーバーの IP アドレス |
| `monitorAddress` | string (IP) | いいえ | ヘルスチェックだけに使う代替バックエンド IP アドレス。省略時は `address` を使います。 |
| `weight` | int (1-100) | いいえ | 希望するトラフィック重み。デフォルト: 1。正の不均等な weight も受け付けて backend spec に保存しますが、現在の VPP LB plugin 経路では metadata のみです。全 backend は weight なしで programming され、実トラフィック分散は重み付きになりません。 |

#### HealthCheckSpec

| フィールド | 型 | 必須 | 説明 |
|----------|-----|------|------|
| `type` | string | はい | プローブタイプ: `http`, `https`, `tcp`, `ping`, `tls-hello` |
| `intervalSeconds` | int (≥1) | いいえ | プローブ間隔。デフォルト: 5 |
| `timeoutSeconds` | int (≥1) | いいえ | 応答待ちの最大時間。デフォルト: 3 |
| `riseCount` | int (≥1) | いいえ | 健全と判定する連続成功回数。デフォルト: 3 |
| `fallCount` | int (≥1) | いいえ | 不健全と判定する連続失敗回数。デフォルト: 2 |
| `http` | HTTPHealthCheck | いいえ | HTTP/HTTPS 固有の設定 |
| `tcp` | TCPHealthCheck | いいえ | TCP/TLS-HELLO 固有の設定 |

注: ヘルスチェックの時間設定は、現在は秒単位の整数として保存・検証されます。現行 API/model では秒未満の interval/timeout は受け付けません。将来の API/model revision でミリ秒単位のヘルスチェック時間設定に対応する予定です。それまでは秒単位の値を指定してください。

#### HTTPHealthCheck

| フィールド | 型 | 必須 | 説明 |
|----------|-----|------|------|
| `port` | int (1-65535) | はい | ターゲットポート |
| `path` | string | いいえ | HTTP パス。デフォルト: `/` |
| `method` | string | いいえ | HTTP メソッド: `GET`, `HEAD`, `POST`。デフォルト: `GET` |
| `host` | string | いいえ | Host ヘッダーのオーバーライド |
| `headers` | map[string]string | いいえ | 追加の HTTP ヘッダー |
| `expectedCodes` | []int | いいえ | 成功を示す HTTP ステータスコード |
| `skipTLSVerify` | bool | いいえ | TLS 証明書検証のスキップ（HTTPS のみ） |

#### TCPHealthCheck

| フィールド | 型 | 必須 | 説明 |
|----------|-----|------|------|
| `port` | int (1-65535) | はい | TCP または TLS-HELLO 接続のターゲットポート |
| `send` | string | いいえ | TCP 接続後に送信するデータ。TLS-HELLO では無視されます。 |
| `expectedResponse` | string | いいえ | TCP 応答に期待する部分文字列。TLS-HELLO では無視されます。 |

### Status フィールド

#### VirtualIPStatus

| フィールド | 型 | 説明 |
|----------|-----|------|
| `observedGeneration` | int64 | Operator が観測した最新の generation |
| `healthyBackends` | int | 健全なバックエンドの数 |
| `totalBackends` | int | 設定されたバックエンドの総数 |
| `backends` | []BackendStatus | バックエンドごとのヘルスステータス |
| `conditions` | []Condition | 標準的な Kubernetes conditions |

#### BackendStatus

| フィールド | 型 | 説明 |
|----------|-----|------|
| `address` | string | バックエンド IP アドレス |
| `healthy` | bool | バックエンドが健全かどうか |
| `lastProbeTime` | Time | 最新のプローブのタイムスタンプ |
| `message` | string | 人間が読めるメッセージ |

### 表示カラム

`kubectl get vip` 使用時：

| カラム | ソース |
|--------|--------|
| NAME | `metadata.name` |
| Address | `spec.address` |
| Port | `spec.port` |
| Protocol | `spec.protocol` |
| Healthy | `status.healthyBackends` |
| Total | `status.totalBackends` |
| Age | `metadata.creationTimestamp` |

### バリデーションルール

CRD スキーマと任意の Admission Webhook が以下を検証します：

- `address` は有効な IP アドレスであること
- `port` は 1〜65535 の範囲であること
- `protocol` は `TCP` または `UDP` であること
- `encapType` は `GRE4`, `GRE6`, `L3DSR`, `NAT4`, `NAT6` のいずれかであること
- `dscp` は指定する場合 1〜63 の範囲であること
- バックエンドの `weight` は 1〜100 の範囲であること
- バックエンドの `address` は有効な IP アドレスであること
- ヘルスチェックの `type` は `http`, `https`, `tcp`, `ping` のいずれかであること
- ヘルスチェックの `type` が `http` または `https` の場合、`http` が必須であること
- ヘルスチェックの `type` が `tcp` の場合、`tcp` が必須であること
- ヘルスチェックの probe port は 1〜65535 の範囲であること
- ヘルスチェックの `timeoutSeconds` は `intervalSeconds` より小さいこと

### 使用例

#### L3DSR + HTTP ヘルスチェック

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
      weight: 1
    - address: 10.0.1.2
      weight: 1
    - address: 10.0.1.3
      weight: 50
  healthCheck:
    type: http
    intervalSeconds: 5
    timeoutSeconds: 3
    riseCount: 3
    fallCount: 2
    http:
      port: 8080
      path: /healthz
      method: GET
      expectedCodes: [200]
```

#### NAT4 + TCP ヘルスチェック

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: db-vip
spec:
  address: 203.0.113.20
  port: 3306
  protocol: TCP
  encapType: NAT4
  backends:
    - address: 10.0.2.1
      weight: 1
    - address: 10.0.2.2
      weight: 1
  healthCheck:
    type: tcp
    intervalSeconds: 10
    timeoutSeconds: 5
    riseCount: 2
    fallCount: 3
    tcp:
      port: 3306
```

#### TLS hello ヘルスチェック

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: tls-vip
spec:
  address: 203.0.113.25
  port: 443
  protocol: TCP
  encapType: NAT4
  backends:
    - address: 10.0.2.10
      weight: 1
  healthCheck:
    type: tls-hello
    intervalSeconds: 10
    timeoutSeconds: 5
    riseCount: 2
    fallCount: 3
    tcp:
      port: 443
```

#### GRE4 + Ping ヘルスチェック

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: dns-vip
spec:
  address: 203.0.113.30
  port: 53
  protocol: UDP
  encapType: GRE4
  backends:
    - address: 10.0.3.1
    - address: 10.0.3.2
  healthCheck:
    type: ping
    intervalSeconds: 3
    timeoutSeconds: 2
    riseCount: 2
    fallCount: 2
```

#### 最小構成（ヘルスチェックなし）

```yaml
apiVersion: arca.io/v1alpha1
kind: VirtualIP
metadata:
  name: simple-vip
spec:
  address: 203.0.113.40
  port: 443
  protocol: TCP
  backends:
    - address: 10.0.4.1
    - address: 10.0.4.2
```
