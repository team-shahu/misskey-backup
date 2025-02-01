FROM postgres:16-alpine

ARG RCLONE_CONFIG_BACKUP_ENDPOINT
ARG RCLONE_CONFIG_BACKUP_ACCESS_KEY_ID
ARG RCLONE_CONFIG_BACKUP_SECRET_ACCESS_KEY
ARG RCLONE_CONFIG_BACKUP_BUCKET_ACL
ARG GOOGLE_DRIVE_BACKUP
ARG GDRIVE_ACCOUNT_FILE
ARG GDRIVE_PARENT_ID

# set timezone
RUN ln -sf /usr/share/zoneinfo/Asia/Tokyo /etc/localtime

# install tools
RUN apk update
RUN apk add curl unzip zstd wget


# rclone
RUN curl https://rclone.org/install.sh | bash


# gdrive
COPY ./config/ /root/
RUN wget https://github.com/glotlabs/gdrive/releases/download/3.9.1/gdrive_linux-x64.tar.gz
RUN tar -xzf gdrive_linux-x64.tar.gz
RUN mv gdrive /usr/local/bin/
RUN chmod +x /usr/local/bin/gdrive


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
COPY ./src/backup-gdrive.sh /root/
RUN chmod +x /root/backup.sh /root/backup-gdrive.sh

RUN mkdir -p /misskey-data/backups

# crontab
RUN mkdir -p /var/spool/cron/crontabs
COPY ./config/crontab /var/spool/cron/crontabs/root
RUN chmod 0644 /var/spool/cron/crontabs/root

CMD sh -c "crond -l 0 -f"
