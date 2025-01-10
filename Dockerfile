FROM postgres:15-alpine

ARG RCLONE_CONFIG_BACKUP_ENDPOINT
ARG RCLONE_CONFIG_BACKUP_ACCESS_KEY_ID
ARG RCLONE_CONFIG_BACKUP_SECRET_ACCESS_KEY
ARG RCLONE_CONFIG_BACKUP_BUCKET_ACL

# install tools
RUN apk update
RUN apk add curl unzip p7zip zstd

# rclone
RUN curl https://rclone.org/install.sh | bash

COPY <<EOF /root/.config/rclone/rclone.conf
[backup]
type = s3
provider = Cloudflare
access_key_id = ${RCLONE_CONFIG_BACKUP_ACCESS_KEY_ID}
secret_access_key = ${RCLONE_CONFIG_BACKUP_SECRET_ACCESS_KEY}
region = auto
endpoint = ${RCLONE_CONFIG_BACKUP_ENDPOINT}
bucket_acl = ${RCLONE_CONFIG_BACKUP_BUCKET_ACL}
EOF

# backup script
COPY ./src/backup.sh /root/
RUN chmod +x /root/backup.sh

RUN mkdir -p /misskey-data/backups

# crontab
RUN mkdir -p /var/spool/cron/crontabs
COPY ./config/crontab /var/spool/cron/crontabs/root
RUN chmod 0644 /var/spool/cron/crontabs/root

CMD sh -c "crond -l 0 -f"