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
- arca-lb バージョン: 
```

### 2. ブランチの作成

```bash
# メインブランチから最新を取得
git checkout main
git pull origin main

# 機能ブランチを作成
git checkout -b feature/my-feature
# または
git checkout -b fix/my-bugfix
```

### 3. コードの変更

- コードスタイルに従う（[開発環境セットアップ](./development.md) を参照）
- テストを追加・更新
- ドキュメントを更新

### 4. コミット

```bash
# 変更をステージング
git add .

# コミット（明確なメッセージを付ける）
git commit -m "feat: add new feature"
# または
git commit -m "fix: fix bug in VIP creation"
```

**コミットメッセージの形式**:

- `feat:` - 新機能
- `fix:` - バグ修正
- `docs:` - ドキュメント
- `test:` - テスト
- `refactor:` - リファクタリング
- `chore:` - その他

### 5. プッシュと Pull Request

```bash
# ブランチをプッシュ
git push origin feature/my-feature
```

GitHub で Pull Request を作成してください。

## Developer Certificate of Origin (DCO)

個人・企業を問わずコントリビューションをしやすくするため、軽量なサインオフ（sign-off）プロセスを採用しています。

コントリビューションを行うことで、あなたの作業が本プロジェクトのライセンスの下で提出され、かつ提出する権利を有していることに同意したものとみなされます。

コミット時にサインオフしてください：

```bash
git commit -s
```

## コードレビュー

### Pull Request のチェックリスト

- [ ] コードが既存のスタイルに従っている
- [ ] テストが追加・更新されている
- [ ] ドキュメントが更新されている
- [ ] リンターエラーがない
- [ ] すべてのテストがパスしている

### レビューの観点

- コードの品質
- テストカバレッジ
- パフォーマンスへの影響
- セキュリティへの影響
- ドキュメントの完全性

## コーディング規約

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

### テストの追加

- 新機能には必ずテストを追加
- バグ修正には回帰テストを追加
- テストカバレッジを維持

### テストの実行

```bash
# すべてのテスト
make test

# 特定のパッケージ
go test ./internal/controller/api/...

# カバレッジ
go test -coverprofile=coverage.out ./...
```

## ドキュメント

### ドキュメントの更新

- 新機能にはドキュメントを追加
- API の変更には API ドキュメントを更新
- 設定の変更には設定ガイドを更新

### ドキュメントの場所

- `docs/` - ユーザードキュメント
- `README.md` - プロジェクト概要

## ライセンス

コントリビューションは MIT ライセンスの下で提供されることに同意したものとみなされます。

## 行動規範

- 建設的なフィードバックを提供
- 他者を尊重
- オープンで包括的なコミュニティを維持

## 質問

質問がある場合は、GitHub Issues で質問してください。

## 次のステップ

- [開発環境セットアップ](./development.md) を参照して、開発を開始します
- [アーキテクチャ詳細](./architecture.md) を参照して、システムの設計を理解します
