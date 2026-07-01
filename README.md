# Misskey Backup Service

Misskeyで利用するPostgreSQLとRedisを自動でバックアップ・管理することを目的としたDocker Image/Toolです🌱

## Features

- 🎀 ワンクリックで暗号化バックアップをR2へアップロード
- 🌤️ リトライ＆並列マルチパートアップロードで安定転送
- 🔎 pg_restoreでダンプ健全性を検証してから保存
- 🗂️ 世代数ベースで古いバックアップを自動整理
- 🍬 復元はURL指定でOK、CLIオプションで鍵を上書き可能
- ☁️ 復元のみならR2設定不要、署名付きURL(既定7日有効)から直接取得

## Prerequisites

- Docker & Docker Compose
- Cloudflare R2アカウントとバケット
- PostgreSQLデータベースへのアクセス権限

## Quick Start

1. クローン＆移動
   ```bash
   git clone https://github.com/team-shahu/misskey-backup.git
   cd misskey-backup
   ```
2. 環境変数テンプレートをコピー
   ```bash
   cp config/env.example config/.env
   ```
3. 暗号化キーを生成して`config/.env`の`BACKUP_ENCRYPTION_KEY`へ設定 (必須、32バイト以上)
   ```bash
   openssl rand -base64 32
   ```
4. コンテナの起動
   ```bash
   docker compose up -d
   ```

## Configuration

### バックアップ対象トグル

PostgreSQLもRedisもデフォルトで有効。片方だけ運用したい場合は該当トグルを`false`にする。

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `POSTGRES_BACKUP_ENABLED` | PostgreSQLバックアップの有効化 | `true` |
| `REDIS_BACKUP_ENABLED` | Redisバックアップの有効化 | `true` |

> [!IMPORTANT]
> Redisバックアップはデフォルトで有効です。PostgreSQLのみ運用を続ける場合は`REDIS_BACKUP_ENABLED=false`を設定してください(未設定だと毎回Redis取得に失敗し失敗通知が飛びます)。有効にする場合はバックアップコンテナからRedisへ到達できるようネットワーク設定も必要です。

### PostgreSQL設定

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `POSTGRES_HOST` | PostgreSQLホスト | `localhost` |
| `POSTGRES_PORT` | PostgreSQLポート | `5432` |
| `POSTGRES_USER` | データベースユーザー | `postgres` |
| `POSTGRES_PASSWORD` | データベースパスワード | - |
| `POSTGRES_DB` | データベース名 | `misskey` |

### Redis設定

`redis-cli --rdb`でネットワーク越しにRDBスナップショットを取得します(全DB含む完全スナップショット)。

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `REDIS_HOST` | Redisホスト | `redis` |
| `REDIS_PORT` | Redisポート | `6379` |
| `REDIS_PASSWORD` | Redisパスワード (未設定なら認証なし) | - |
| `REDIS_TLS` | TLS接続を有効化 | `false` |

### バックアップ設定

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `BACKUP_DIR` | バックアップ保存ディレクトリ | `/app/backups` |
| `BACKUP_GENERATIONS` | 保持する世代数(直近N本) | `30` |
| `COMPRESSION_LEVEL` | zstd圧縮レベル (1-19) | `3` |
| `BACKUP_ENCRYPTION_KEY` | バックアップ暗号化キー、32バイト以上 (必須) | - |

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
| `RETRY_BASE_DELAY` | 基本遅延時間(秒) | `1` |
| `RETRY_MAX_DELAY` | 最大遅延時間(秒) | `30` |

### アップロード設定

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `UPLOAD_TIMEOUT` | アップロードタイムアウト時間(分) | `60` |
| `CHUNK_SIZE` | マルチパートアップロードのチャンクサイズ(MB) | `10` |
| `MAX_CONCURRENCY` | マルチパートアップロードの並列数 | `5` |

### 通知設定

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `NOTIFICATION` | Discord通知の有効化 | `false` |
| `DISCORD_WEBHOOK_URL` | Discord Webhook URL | - |
| `NOTIFY_INCLUDE_URL` | 通知にダウンロードURLを含める | `true` |

### スケジューラー設定

| 項目 | 説明 | デフォルト値 |
|------|------|-------------|
| `CRON_SCHEDULE` | Cron形式のスケジュール | `0 5,17 * * *` |
| `TZ` | タイムゾーン | `Asia/Tokyo` |

## ヘルスチェック

コンテナは`:8080/health`にヘルスチェック用HTTPエンドポイントを公開します(`compose.yaml`の`healthcheck`が参照)。

## バックアップファイル

バックアップファイルは以下の形式で保存されます：

```
# PostgreSQL
{データベース名}_{日付}_{時刻}.dump.zst.enc

# Redis
redis_{日付}_{時刻}.rdb.zst.enc
```

例: `misskey_2025-08-28_21-35.dump.zst.enc` / `redis_2025-08-28_21-35.rdb.zst.enc`

世代整理(`BACKUP_GENERATIONS`)はターゲットごとに独立して適用されます。

## 使い方

通知に載る署名付きURL(またはR2の公開URL)を指定して、暗号化済みバックアップを復元できます。復元先は `./restore` に展開され、最終的に `.dump` ができます。

```bash
cd src
# 環境変数で鍵を渡す場合
BACKUP_ENCRYPTION_KEY=... ./misskey-backup --restore-url "https://backup.example.com/path/to/misskey_2025-08-28_21-35.dump.zst.enc"

# CLIオプションで鍵を渡す場合(環境変数より優先)
./misskey-backup --restore-url "https://backup.example.com/path/to/misskey_2025-08-28_21-35.dump.zst.enc" \
  --encryption-key "your-hex-or-base64-key"
```

- 暗号化キーは環境変数`BACKUP_ENCRYPTION_KEY`または`--encryption-key`のどちらかを指定する必要があります(通常、CLIオプション側が優先されます)。
- 復元のみの場合はR2設定は不要です(URLへ直接GETできる前提。署名付きURLは発行から既定7日間有効)。

## Logs

アプリケーションは以下の情報をログに出力します：

- バックアップの開始と完了
- ファイルサイズと実行時間
- アップロード方式の自動選択(単一/マルチパート)
- マルチパートアップロードの進行状況(チャンクサイズ、パート数)
- エラーが発生した場合は詳細なエラーメッセージ
- R2アップロードの結果
- リトライ実行時の詳細なログ(遅延時間、試行回数など)
- アップロードタイムアウト設定の適用状況

## License

このプロジェクトはMITライセンスの下で公開されています。

## Contributing

Pull requests and issue reports are welcome. Before contributing, please check the following steps:

1. Create a fork
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit changes (`git commit -m 'feat: add amazing feature'`)
4. Push the branch (`git push origin feature/amazing-feature`)
5. Create a pull request
