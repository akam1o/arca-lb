# arca-lb コントリビューションガイド

このドキュメントでは、arca-lb プロジェクトへの貢献方法を説明します。

## コントリビューションの種類

以下のような貢献を歓迎します：

- バグレポート
- 機能要望
- ドキュメントの改善
- コードの改善
- テストの追加

## 開発プロセス

### 1. Issue の作成

バグレポートや機能要望は、GitHub Issues で作成してください。

**バグレポートのテンプレート**:

```markdown
## バグの説明
簡潔にバグを説明してください。

## 再現手順
1. ...
2. ...
3. ...

## 期待される動作
何が起こるべきか説明してください。

## 実際の動作
何が起こったか説明してください。

## 環境
- OS:
- Go バージョン:
- Kubernetes バージョン:
- arca-lb バージョン:
```

### 2. ブランチの作成

```bash
# メインブランチから最新を取得
git checkout main
git pull origin main

# フィーチャーブランチの作成
git checkout -b feature/my-feature
# または
git checkout -b fix/my-bugfix
```

### 3. 変更の実装

- コードスタイルに従ってください（[開発環境](./development.ja.md) 参照）。
- テストを追加または更新してください。
- ドキュメントを更新してください。
- CRD 型を変更した場合は `make manifests generate` を実行してください。

### 4. コミット

```bash
# 変更をステージ
git add .

# 明確なメッセージでコミット
git commit -m "feat: 新機能の追加"
# または
git commit -m "fix: VIP 作成時のバグを修正"
```

**コミットメッセージのプレフィックス**:

- `feat:` - 新機能
- `fix:` - バグ修正
- `docs:` - ドキュメント
- `test:` - テスト
- `refactor:` - リファクタリング
- `chore:` - その他

### 5. プッシュとプルリクエスト

```bash
# ブランチをプッシュ
git push origin feature/my-feature
```

その後、GitHub でプルリクエストを作成してください。

## Developer Certificate of Origin (DCO)

個人および企業からの貢献を容易にするため、軽量なサインオフプロセスを使用しています。

貢献することで、その作品がプロジェクトのライセンスの下で提出され、提出する権利があることに同意するものとします。

コミットにサインオフしてください：

```bash
git commit -s
```

## コードレビュー

### プルリクエストのチェックリスト

- [ ] コードが既存のスタイルに従っている
- [ ] テストが追加または更新されている
- [ ] ドキュメントが更新されている
- [ ] リンターエラーがない
- [ ] 全テストがパスする
- [ ] CRD マニフェストが再生成されている（型を変更した場合）: `make manifests generate`

### レビューの重点

- コード品質
- テストカバレッジ
- パフォーマンスへの影響
- セキュリティへの影響
- ドキュメントの完全性

## コーディング規約

### Go ガイドライン

- [Effective Go](https://go.dev/doc/effective_go) に従う
- `gofmt` でフォーマット
- `golangci-lint` で品質チェック

### 命名規則

- **パッケージ**: 小文字、単数形
- **型**: PascalCase
- **関数**: PascalCase（公開）、camelCase（非公開）
- **定数**: PascalCase または UPPER_SNAKE_CASE

### エラーハンドリング

```go
// エラーを明示的にハンドリング
if err != nil {
    return fmt.Errorf("context: %w", err)
}
```

### ロギング

```go
// 構造化ロギングを使用（log/slog）
slog.Info("VIP reconciled",
    "vip", vipName,
    "backends", len(backends),
)

slog.Error("failed to apply VIP",
    "vip", vipName,
    "error", err,
)
```

## テスト

### テストの追加

- 新機能にはテストを追加してください。
- バグ修正にはリグレッションテストを追加してください。
- カバレッジを維持してください。

### テストの実行

```bash
# 全テスト
make test

# 特定のパッケージ
go test ./internal/operator/...

# Agent テスト
go test ./internal/agent/dataplane/ ./internal/agent/routing/ ./internal/agent/store/

# カバレッジ
go test -coverprofile=coverage.out ./...
```

## ドキュメント

### ドキュメントの更新

- 新機能にはドキュメントを追加してください。
- CRD スキーマの変更には API ドキュメントを更新してください。
- 設定の変更には設定ドキュメントを更新してください。
- 英語版と日本語版の両方を同期してください。

### ドキュメントの場所

- `docs/` - ユーザードキュメント（英語 + 日本語のペア）
- `README.md` / `README.ja.md` - プロジェクト概要
- `SPEC.md` - 技術仕様

## ライセンス

コントリビューションは Apache License 2.0 の下で提供されます。[LICENSE](../LICENSE) を参照してください。

## 行動規範

- 建設的なフィードバックを提供してください。
- 敬意を持って接してください。
- オープンで包括的なコミュニティを維持してください。
