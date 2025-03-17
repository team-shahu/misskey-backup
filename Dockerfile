FROM debian:trixie-slim

ARG RCLONE_CONFIG_BACKUP_ENDPOINT
ARG RCLONE_CONFIG_BACKUP_ACCESS_KEY_ID
ARG RCLONE_CONFIG_BACKUP_SECRET_ACCESS_KEY
ARG RCLONE_CONFIG_BACKUP_BUCKET_ACL
ARG GOOGLE_DRIVE_BACKUP
ARG GDRIVE_ACCOUNT_FILE
ARG GDRIVE_PARENT_ID

# install tools
RUN apt-get update && apt-get install curl unzip zstd wget postgresql -y && apt-get install -y cron --no-install-recommends

# rclone
RUN curl https://rclone.org/install.sh | bash


# gdrive
COPY ./config/ /root/
RUN wget https://github.com/glotlabs/gdrive/releases/download/3.9.1/gdrive_linux-x64.tar.gz \
    && tar -xzf gdrive_linux-x64.tar.gz \
    && mv gdrive /usr/local/bin/ \
    && chmod +x /usr/local/bin/gdrive 

RUN mkdir /tools
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
COPY ./src/backup.sh /tools/
COPY ./src/backup-gdrive.sh /tools/
RUN chmod +x /tools/backup.sh /tools/backup-gdrive.sh && mkdir -p /misskey-data/backups

# crontab
RUN /etc/init.d/cron start
COPY ./config/crontab /etc/cron.d/crontab
RUN chmod 0644 /etc/cron.d/crontab
RUN crontab /etc/cron.d/backup
