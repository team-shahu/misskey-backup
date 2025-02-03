# misskey-backup
postgreSQLのバックアップをよしなに取るためのスクリプト  
  
## 使い方
1. `git clone https://github.com/team-shahu/misskey-backup.git`  
2. `cd misskey-backup`  
3. `cp ./config/.env.example ./config/.env && cp ./config/crontab.example ./config/crontab`  
4. `./config/.env`を編集  
4. `./config/crontab`を編集  
5. (バックアップ先としてGoogle Driveを併用する場合)  
    1. [LOCAL] [この手順](https://github.com/glotlabs/gdrive/blob/main/docs/create_google_api_credentials.md)に従ってGoogle Drive APIの認証情報を取得  
    2. [LOCAL] [gdriveを導入](https://github.com/glotlabs/gdrive/blob/main/docs/create_google_api_credentials.md)  
    3. [LOCAL] `gdrive account add`を実行して[認証情報を登録](https://github.com/glotlabs/gdrive/blob/main/docs/create_google_api_credentials.md)  
    4. [LOCAL] `account=$(gdrive account list) && gdrive account export ${account}`を実行して認証情報を.tar形式でエクスポート  
    5. [REMOTE] エクスポートしたファイルを./config/配下に配置  
    6. [REMOTE] `./config/.env`の`GDRIVE_ACCOUNT_FILE`にエクスポートしたファイル名、`GDRIVE_PARENT_ID`にバックアップ先のGoogle DriveのフォルダIDを記述  
    7. [REMOTE] `./config/.env`の`GOOGLE_DRIVE_BACKUP`を`true`に変更  
    8. [REMOTE] `./config/crontab`を編集。指定の場所のコメントアウトを外す。  
6. `docker compose up -d --build`  
  

> [!NOTE]
> ネットワークがない旨のエラーが発生することがあります。以下のコマンドを実行し、ネットワークを作成後、再度`docker compose up -d --build`を実行してください。
> ```bash
> docker network create misskey-backup_default
> ```
