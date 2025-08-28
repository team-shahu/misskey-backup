# Misskey Backup Service

MisskeyのPostgreSQLデータベースを自動バックアップし、Cloudflare R2ストレージに保存するGoアプリケーションです。

## 機能

- **PostgreSQLバックアップ**: `pg_dump`を使用したカスタム形式でのデータベースダンプ
- **ファイル圧縮**: `zstd`による高効率な圧縮
- **クラウドストレージ**: Cloudflare R2への自動アップロード
- **リトライ機能**: 指数バックオフによる堅牢なエラーハンドリング
- **スケジュール実行**: Cron形式での自動バックアップ実行
- **通知機能**: Discord Webhookによるバックアップ結果通知
- **古いバックアップの自動削除**: 設定可能な保持期間

## 前提条件

- Docker & Docker Compose
- Cloudflare R2アカウントとバケット
- PostgreSQLデータベースへのアクセス権限

## セットアップ

### 1. リポジトリのクローン

```bash
git clone <repository-url>
cd misskey-backup
```

### 2. 環境変数の設定

```bash
cp config/env.example config/.env
```
`config/.env`ファイルを編集して、設定を行ってください：


## 設定項目

### PostgreSQL設定

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `POSTGRES_HOST` | PostgreSQLホスト | `localhost` |
| `POSTGRES_PORT` | PostgreSQLポート | `5432` |
| `POSTGRES_USER` | データベースユーザー | `postgres` |
| `POSTGRES_PASSWORD` | データベースパスワード | - |
| `POSTGRES_DB` | データベース名 | `misskey` |

### バックアップ設定

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `BACKUP_DIR` | バックアップ保存ディレクトリ | `/app/backups` |
| `BACKUP_RETENTION` | バックアップ保持日数 | `30` |
| `COMPRESSION_LEVEL` | zstd圧縮レベル (1-19) | `3` |

### Cloudflare R2設定

| 項目 | 説明 |
|------|------|
| `BACKUP_ENDPOINT` | R2エンドポイントURL |
| `BACKUP_ACCESS_KEY_ID` | R2アクセスキーID |
| `BACKUP_SECRET_ACCESS_KEY` | R2シークレットアクセスキー |
| `R2_BUCKET_NAME` | R2バケット名 |
| `R2_PREFIX` | バックアップファイルのプレフィックス |
| `BACKUP_BUCKET_ACL` | バケットのACL設定 |

### リトライ設定

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `MAX_RETRIES` | 最大リトライ回数 | `5` |
| `RETRY_BASE_DELAY` | 基本遅延時間（秒） | `1` |
| `RETRY_MAX_DELAY` | 最大遅延時間（秒） | `30` |

### アップロード設定

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `UPLOAD_TIMEOUT` | アップロードタイムアウト時間（分） | `60` |
| `CHUNK_SIZE` | マルチパートアップロードのチャンクサイズ（MB） | `50` |

### スケジューラー設定

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `CRON_SCHEDULE` | Cron形式のスケジュール | `0 5,17 * * *` |
| `TZ` | タイムゾーン | `Asia/Tokyo` |

## バックアップファイル

バックアップファイルは以下の形式で保存されます：

```
{データベース名}_{日付}_{時刻}.dump.zst
```

例: `misskey_2025-08-28_21-35.dump.zst`

## ログ

アプリケーションは以下の情報をログに出力します：

- バックアップの開始と完了
- ファイルサイズと実行時間
- アップロード方式の自動選択（単一/マルチパート）
- マルチパートアップロードの進行状況（チャンクサイズ、パート数）
- エラーが発生した場合は詳細なエラーメッセージ
- R2アップロードの結果
- リトライ実行時の詳細なログ（遅延時間、試行回数など）
- アップロードタイムアウト設定の適用状況


## ライセンス

このプロジェクトはMITライセンスの下で公開されています。

## 貢献

プルリクエストやイシューの報告を歓迎します。貢献する前に、以下の手順を確認してください：

1. フォークを作成
2. 機能ブランチを作成 (`git checkout -b feature/amazing-feature`)
3. 変更をコミット (`git commit -m 'feat: 機能追加の概要'`)
4. ブランチにプッシュ (`git push origin feature/amazing-feature`)
5. プルリクエストを作成
