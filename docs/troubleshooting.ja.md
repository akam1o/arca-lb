# arca-lb トラブルシューティング

このドキュメントでは、arca-lb の一般的な問題とその解決方法を説明します。

## Operator の問題 (v2)

### Operator が起動しない

**症状**: Operator Pod が CrashLoopBackOff になる。

**原因と解決方法**:

1. **CRD が未インストール**
   - CRD をインストール: `kubectl apply -f config/crd/bases/`
   - 確認: `kubectl get crd virtualips.arca.io`

2. **RBAC の設定ミス**
   - RBAC を適用: `kubectl apply -f config/rbac/`
   - ServiceAccount の権限を確認。

3. **イメージが見つからない**
   - Deployment のイメージ名とタグを確認。
   - 手動プル: `docker pull <image>`

### VirtualIP のステータスが更新されない

**症状**: VirtualIP にステータスが表示されない、または古いステータスのまま。

**原因と解決方法**:

1. **Operator が動作していない**
   - Pod の状態を確認: `kubectl get pods -l app.kubernetes.io/name=arca-lb-operator`
   - ログを確認: `kubectl logs -l app.kubernetes.io/name=arca-lb-operator`

2. **RBAC の問題**
   - Operator は `virtualips/status` の `update` 権限が必要。
   - `config/rbac/role.yaml` を確認。

3. **Finalizer が残っている**
   - メタデータを確認: `kubectl get vip <name> -o yaml`
   - 必要に応じて finalizer を削除: `kubectl edit vip <name>`

### Admission バリデーションが失敗する

**症状**: `kubectl apply` でアドミッションエラーが返される。

**原因と解決方法**:

1. **不正なフィールド値**
   - エラーメッセージで具体的なフィールドを確認。
   - 有効な値については [API リファレンス](./api.ja.md) を参照。

2. **任意の Webhook が未登録**
   - CRD スキーマバリデーションは CRD 適用時点で常に有効です。
   - `--enable-webhooks` を有効化している場合は、Webhook 設定と証明書が有効か確認。

## Agent の問題

### Agent が起動しない

**症状**: Agent Pod が CrashLoopBackOff になる、または起動に失敗する。

**原因と解決方法**:

1. **設定ファイルが見つからない**
   - ConfigMap のマウント先を確認: `/etc/arca-lb/agent.yaml`

2. **Kubernetes API 接続エラー**
   - Agent の ServiceAccount の RBAC を確認。
   - kubeconfig / in-cluster 認証を確認。

3. **VPP 接続エラー**
   - VPP が動作中か確認: `systemctl status vpp`
   - VPP ソケットパスを確認: `/run/vpp/api.sock`
   - DaemonSet spec でのソケットマウントを確認。

4. **権限の問題**
   - Agent は VPP ソケットへのアクセスが必要。
   - Pod が適切な権限で実行されているか確認。

### VIP が VPP に適用されない

**症状**: Kubernetes で VirtualIP が作成されているが VPP に反映されない。

**原因と解決方法**:

1. **Watcher がイベントを受信していない**
   - Agent ログで watch エラーを確認。
   - `kubernetes.namespace` の設定を確認。
   - `kubernetes.resyncInterval` の設定を確認。

2. **Reconciler のエラー**
   - Agent ログでリコンシリエーションエラーを確認。
   - DataPlane / Router のエラーを確認。

3. **VPP LB プラグインの問題**
   - VPP LB プラグインが有効か確認。
   - VPP の設定を確認: `vppctl show lb vip`

### ヘルスチェックが失敗する

**症状**: バックエンドのヘルスチェックが失敗と報告される。

**原因と解決方法**:

1. **ネットワーク接続性**
   - Agent からバックエンドへの接続を確認。
   - ファイアウォールルールを確認。

2. **ヘルスチェック設定**
   - VirtualIP CR のヘルスチェック設定を確認（port、path、expectedCodes）。
   - VirtualIP のステータスを確認: `kubectl get vip <name> -o yaml`

3. **タイムアウト設定**
   - タイムアウトが短すぎないか確認。
   - VirtualIP spec の `healthCheck.timeoutSeconds` を調整。

### FRR BGP 経路広報が機能しない

**症状**: VIP 経路が BGP で広報されない。

**原因と解決方法**:

1. **FRR が動作していない**
   - FRR が動作中か確認: `systemctl status frr`
   - Agent 設定で `routing.type: frr` になっているか確認。

2. **vtysh が見つからない**
   - Agent 設定の `routing.frr.vtyshPath` を確認。
   - デフォルト: `/usr/bin/vtysh`

3. **BGP ピア設定**
   - BGP ピアの設定が正しいか確認。
   - 確認: `vtysh -c "show bgp summary"`

## メトリクスの問題

### Prometheus メトリクスを取得できない

**症状**: `/metrics` エンドポイントにアクセスできない。

**原因と解決方法**:

1. **メトリクスが無効**
   - Agent 設定で `metrics.enabled: true` になっているか確認。

2. **ポートの問題**
   - メトリクスのポートを確認（デフォルト: `:9090`）。
   - ファイアウォールルールを確認。

3. **メトリクスサーバーが起動していない**
   - Agent ログでメトリクスサーバーのエラーを確認。

## ログの確認

### Operator ログ

```bash
# Operator ログのストリーミング
kubectl logs -f -l app.kubernetes.io/name=arca-lb-operator

# 詳細レベルを上げる
kubectl logs -f -l app.kubernetes.io/name=arca-lb-operator -- --zap-log-level=debug
```

### Agent ログ (v2)

```bash
# Agent ログ（Kubernetes）
kubectl logs -f -l app.kubernetes.io/name=arca-lb-agent

# スタンドアロン Agent
sudo ./bin/arcalb-agent --config deploy/config/agent.example.yaml
```

### VPP ログ

```bash
# systemd の場合
journalctl -u vpp -f

# 直接ログファイル
tail -f /var/log/vpp/vpp.log

# VPP の状態確認
vppctl show lb vip verbose
vppctl show lb as
```

### FRR ログ

```bash
# systemd の場合
journalctl -u frr -f

# 直接ログファイル
tail -f /var/log/frr/frr.log
```

## kubectl でのデバッグ

```bash
# 全 VIP をステータス付きで表示
kubectl get vip -o wide

# 特定の VIP を詳しく確認
kubectl get vip web-vip -o yaml

# 変更の監視
kubectl get vip -w

# イベントの確認
kubectl describe vip web-vip

# Agent Pod の確認
kubectl get pods -l app.kubernetes.io/name=arca-lb-agent -o wide

# Operator Pod の確認
kubectl get pods -l app.kubernetes.io/name=arca-lb-operator
```

## FAQ

### Q: VirtualIP が「Not Ready」のままになる

**A**: 以下を確認してください：

1. Agent が正しいネームスペースを監視しているか。
2. VPP 接続が健全か（Agent ログ）。
3. バックエンドが健全か（VIP ステータスを確認）。
4. Operator ログでリコンシリエーションエラーがないか。

### Q: VIP 作成後にトラフィックが流れない

**A**: 以下を確認してください：

1. VIP が VPP に設定されているか: `vppctl show lb vip`
2. バックエンドが追加されているか: `vppctl show lb as`
3. BGP 経路が広報されているか（FRR 使用時）: `vtysh -c "show ip route"`
4. バックエンドが健全で稼働中か。
5. バックエンドサーバーが正しく設定されているか（[バックエンド設定](./backend-setup.ja.md) 参照）。
