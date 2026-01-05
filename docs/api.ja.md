# arca-lb REST API リファレンス

このドキュメントでは、arca-lb Controller の REST API について説明します。

## ベース URL

```
http://localhost:8080
```

## 認証

現在のバージョンでは認証は実装されていません。本番環境では適切な認証・認可を実装してください。

## エンドポイント

### ヘルスチェック

#### GET /healthz

ヘルスチェックエンドポイント。サーバーが正常に動作しているかを確認します。

**レスポンス**

```json
{
  "status": "healthy",
  "time": "2025-12-20T10:00:00Z"
}
```

#### GET /readyz

レディネスチェックエンドポイント。サーバーがリクエストを受け付ける準備ができているかを確認します。

**レスポンス**

- **200 OK**: サーバーが準備完了
  ```json
  {
    "status": "ready",
    "time": "2025-12-20T10:00:00Z"
  }
  ```

- **503 Service Unavailable**: サーバーが準備未完了（データストア接続エラーなど）
  ```json
  {
    "status": "not ready",
    "error": "datastore unavailable"
  }
  ```

### VIP 管理

#### POST /api/v1/vips

新しい VIP を作成します。

**リクエストボディ**

```json
{
  "vip": "192.168.1.100",
  "port": 80,
  "protocol": "TCP",
  "lb_method": "maglev",
  "encap_type": "L3DSR",
  "dscp": 10,
  "health_check": {
    "type": "http",
    "interval": "10s",
    "timeout": "5s"
  }
}
```

**注意**: 
- `health_check.type` は小文字（`http`, `https`, `tcp`, `ping`）
- `health_check.interval` と `health_check.timeout` は Go の duration 文字列（例: `10s`, `1m`）
- `rise_count` / `fall_count` は現在 REST API からは設定できません（デフォルト: 3/3）
- VIP 作成時は `health_check.vip_id` は不要（VIP 作成後に自動的に設定されます）
- `encap_type` は任意（`GRE4`, `GRE6`, `L3DSR`, `NAT4`, `NAT6`）。省略した場合は Agent のデフォルト（`vpp.lb.encap_type`）が使用されます
- `dscp` は任意（1-63）で、`encap_type` が `L3DSR`（DSCP 方式）に解決される場合のみ使用されます（GRE/NAT では使用されません）。省略した場合は Agent のデフォルト（`vpp.lb.dscp`）が使用されます（`L3DSR` の場合 `dscp=0` はエラーになります）

**レスポンス**

- **201 Created**: VIP が正常に作成された
- **400 Bad Request**: リクエストが不正
- **409 Conflict**: VIP が既に存在する

#### GET /api/v1/vips

すべての VIP の一覧を取得します。

**レスポンス**

```json
{
  "vips": [
    {
      "id": "vip-1",
      "vip": "192.168.1.100",
      "port": 80,
      "protocol": "TCP",
      "lb_method": "maglev",
      "encap_type": "L3DSR",
      "dscp": 10,
      "created_at": "2025-12-20T10:00:00Z",
      "updated_at": "2025-12-20T10:00:00Z"
    }
  ],
  "count": 1
}
```

#### GET /api/v1/vips/:id

指定された ID の VIP を取得します。

**パラメータ**

- `id` (path): VIP ID

**レスポンス**

- **200 OK**: VIP が見つかった
- **404 Not Found**: VIP が見つからない

#### PUT /api/v1/vips/:id

指定された ID の VIP を更新します。

**パラメータ**

- `id` (path): VIP ID

**リクエストボディ**

```json
{
  "vip": "192.168.1.101",
  "port": 443,
  "protocol": "TCP",
  "lb_method": "maglev",
  "encap_type": "GRE4",
  "dscp": 0
}
```

**レスポンス**

- **200 OK**: VIP が正常に更新された
- **400 Bad Request**: リクエストが不正
- **404 Not Found**: VIP が見つからない

#### DELETE /api/v1/vips/:id

指定された ID の VIP を削除します。

**パラメータ**

- `id` (path): VIP ID

**レスポンス**

- **200 OK**: VIP が正常に削除された
- **404 Not Found**: VIP が見つからない

### Backend 管理

#### POST /api/v1/backends

新しいバックエンドを追加します。

**リクエストボディ**

```json
{
  "vip_id": "vip-1",
  "ip": "10.0.0.1",
  "weight": 10
}
```

**レスポンス**

- **201 Created**: バックエンドが正常に作成された
- **400 Bad Request**: リクエストが不正
- **404 Not Found**: VIP が見つからない

#### GET /api/v1/backends?vip_id=:vip_id

指定された VIP に属するバックエンドの一覧を取得します。

**クエリパラメータ**

- `vip_id` (required): VIP ID

**レスポンス**

```json
{
  "backends": [
    {
      "id": "backend-1",
      "vip_id": "vip-1",
      "ip": "10.0.0.1",
      "weight": 10
    }
  ],
  "count": 1
}
```

#### GET /api/v1/backends/:id

指定された ID のバックエンドを取得します。

**パラメータ**

- `id` (path): Backend ID

**レスポンス**

- **200 OK**: バックエンドが見つかった
- **404 Not Found**: バックエンドが見つからない

#### PUT /api/v1/backends/:id

指定された ID のバックエンドを更新します。

**パラメータ**

- `id` (path): Backend ID

**リクエストボディ**

```json
{
  "ip": "10.0.0.2",
  "weight": 20
}
```

**レスポンス**

- **200 OK**: バックエンドが正常に更新された
- **400 Bad Request**: リクエストが不正
- **404 Not Found**: バックエンドが見つからない

#### DELETE /api/v1/backends/:id

指定された ID のバックエンドを削除します。

**パラメータ**

- `id` (path): Backend ID

**レスポンス**

- **200 OK**: バックエンドが正常に削除された
- **404 Not Found**: バックエンドが見つからない

### リビジョン管理

#### GET /api/v1/revision

現在の設定リビジョンを取得します。

**レスポンス**

```json
{
  "revision": 123
}
```

## エラーレスポンス

エラーが発生した場合、以下の形式でレスポンスが返されます：

```json
{
  "error": "error message"
}
```

### HTTP ステータスコード

- **200 OK**: リクエストが正常に処理された
- **201 Created**: リソースが正常に作成された
- **400 Bad Request**: リクエストが不正
- **404 Not Found**: リソースが見つからない
- **409 Conflict**: リソースが既に存在する
- **500 Internal Server Error**: サーバー内部エラー

## データモデル

### VIP

```json
{
  "id": "string",
  "vip": "string (IP address)",
  "port": "integer (1-65535)",
  "protocol": "TCP | UDP",
  "lb_method": "maglev",
  "health_check": "HealthCheck (optional)",
  "created_at": "string (RFC3339)",
  "updated_at": "string (RFC3339)"
}
```

### Backend

```json
{
  "id": "string",
  "vip_id": "string",
  "ip": "string (IP address)",
  "weight": "integer (1-100)"
}
```

### HealthCheck

```json
{
  "id": "string",
  "vip_id": "string (VIP 作成時は不要、取得時は存在)",
  "type": "http | https | tcp | ping (required)",
  "interval_sec": "integer (required, min=1)",
  "timeout_sec": "integer (required, min=1)",
  "rise_count": "integer (required, min=1)",
  "fall_count": "integer (required, min=1)",
  "config": {
    "port": "integer (HTTP/HTTPS/TCP only)",
    "path": "string (HTTP/HTTPS only)",
    "method": "string (HTTP/HTTPS only)",
    "expected_codes": "array of integers (HTTP/HTTPS only)"
  },
  "created_at": "string (RFC3339)",
  "updated_at": "string (RFC3339)"
}
```

**注意**: 
- `type` は小文字（`http`, `https`, `tcp`, `ping`）
- `vip_id` は読み取り専用フィールドです。VIP 作成時には不要（VIP 作成後に自動的に設定されます）。VIP 取得時には存在します
- REST API で VIP を作成する場合は `health_check.interval` / `health_check.timeout`（duration 文字列）を使用してください。保存モデルでは `interval_sec` / `timeout_sec` が使われます

## 使用例

### cURL での使用例

#### VIP の作成

```bash
curl -X POST http://localhost:8080/api/v1/vips \
  -H "Content-Type: application/json" \
  -d '{
    "vip": "192.168.1.100",
    "port": 80,
    "protocol": "TCP",
    "lb_method": "maglev"
  }'
```

#### バックエンドの追加

```bash
curl -X POST http://localhost:8080/api/v1/backends \
  -H "Content-Type: application/json" \
  -d '{
    "vip_id": "vip-1",
    "ip": "10.0.0.1",
    "weight": 10
  }'
```

#### VIP の一覧取得

```bash
curl http://localhost:8080/api/v1/vips
```

## 次のステップ

- [設定ガイド](./configuration.ja.md) を参照して、詳細な設定を行います
- [トラブルシューティング](./troubleshooting.ja.md) を参照して、問題の解決方法を確認します
