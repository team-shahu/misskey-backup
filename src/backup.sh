#!/bin/sh

BACKUP_FILE="/misskey-data/backups/${POSTGRES_DB}_$(TZ='Asia/Tokyo' date +%Y-%m-%d_%H-%M).sql"
COMPRESSED="${BACKUP_FILE}.7z"

pg_dump -h $POSTGRES_HOST -U $POSTGRES_USER -d $POSTGRES_DB > $BACKUP_FILE 2>> /var/log/cron.log

7z a $COMPRESSED $BACKUP_FILE

rclone copy --s3-upload-cutoff=5000M --multi-thread-cutoff 5000M $COMPRESSED backup:${R2_PREFIX}

# 成功確認
if [ $? -eq 0 ]; then
    # バックアップファイルを削除
    echo "Backup succeeded" >> /var/log/cron.log
    rm $BACKUP_FILE $COMPRESSED
    # 成功通知
    if [ -n "$NOTIFICATION" ]; then
        curl -X POST -F content="✅バックアップが完了しました。(${COMPRESSED})" ${DISCORD_WEBHOOK_URL} &> /dev/null
    fi
else
    # 失敗時
    echo "Backup failed" >> /var/log/cron.log
    # 通知設定の有無を確認
    if [ -n "$NOTIFICATION" ]; then
        curl -X POST -F content="❌バックアップに失敗しました。ログを確認してください。" ${DISCORD_WEBHOOK_URL} &> /dev/null
    fi
fi
