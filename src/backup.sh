#!/bin/sh
START_TIME=`date +%s`

BACKUP_FILE="/misskey-data/backups/${POSTGRES_DB}_$(TZ='Asia/Tokyo' date +%Y-%m-%d_%H-%M).sql"
COMPRESSED="${BACKUP_FILE}.zst"

set -o errexit
set -o pipefail
set -o nounset

{
    # PostgreSQLのバックアップ
    pg_dump -h $POSTGRES_HOST -U $POSTGRES_USER -d $POSTGRES_DB > $BACKUP_FILE

    # ファイルが存在していれば圧縮
    if [ -f $BACKUP_FILE ]; then
        zstd -f $BACKUP_FILE
    else
        echo "Backup file not found"
        exit 1
    fi

    # ファイルが存在していればアップロード
    if [ -f $COMPRESSED ]; then
        rclone copy --s3-upload-cutoff=5000M --multi-thread-cutoff 5000M $COMPRESSED backup:${R2_PREFIX}
    else
        echo "Compressed file not found"
        exit 1
    fi

    END_TIME=`date +%s`
    TIME=$((END_TIME - START_TIME))

    echo "Backup succeeded"
    # 成功通知
    if [ -n "${NOTIFICATION}" ]; then
        curl -H "Content-Type: application/json" \
             -X POST \
             -d '{
                    "embeds": [
                        {
                            "title": "✅ バックアップが完了しました。",
                            "description": "PostgreSQLのバックアップが正常に完了しました",
                            "color": 5620992,
                            "fields": [
                                {
                                    "name": ":file_folder: 保存先",
                                    "value": "'"${COMPRESSED##*/}"'",
                                    "inline": true
                                },
                                {
                                    "name": ":timer: 実行時間",
                                    "value": "'"${TIME}"'s",
                                    "inline": true
                                }
                            ]
                        }
                    ]
                  }' \
             "${DISCORD_WEBHOOK_URL}"
    fi
} || {
    # 失敗時
    echo "Backup failed"
    # 通知設定の有無を確認
    if [ -n "${NOTIFICATION}" ]; then
        curl -H "Content-Type: application/json" \
             -X POST \
             -d '{
                    "embeds": [
                        {
                            "title": "❌ バックアップに失敗しました。",
                            "description": "PostgreSQLのバックアップが異常終了しました。ログを確認してください。",
                            "color": 15548997,
                        },
                        {
                            "name": ":timer: 実行時間",
                            "value": "'"${TIME}"'s",
                            "inline": true
                        }
                    ]
                  }' \
             "${DISCORD_WEBHOOK_URL}"
    fi
}

# バックアップファイルを削除
rm -rf $BACKUP_FILE
rm -rf $COMPRESSED
