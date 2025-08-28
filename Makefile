.PHONY: help build run test clean docker-build docker-run docker-stop

# デフォルトターゲット
help:
	@echo "利用可能なコマンド:"
	@echo "  build        - Goアプリケーションをビルド"
	@echo "  run          - ローカルでアプリケーションを実行"
	@echo "  test         - テストを実行"
	@echo "  clean        - ビルドファイルを削除"
	@echo "  docker-build - Dockerイメージをビルド"
	@echo "  docker-run   - Dockerコンテナを起動"
	@echo "  docker-stop  - Dockerコンテナを停止"
	@echo "  deps         - 依存関係を更新"

# Goアプリケーションのビルド
build:
	@echo "Goアプリケーションをビルド中..."
	cd src && go build -o misskey-backup .

# ローカルでの実行
run:
	@echo "アプリケーションを実行中..."
	cd src && go run main.go

# テストの実行
test:
	@echo "テストを実行中..."
	cd src && go test -v ./...

# ビルドファイルの削除
clean:
	@echo "ビルドファイルを削除中..."
	rm -f src/misskey-backup

# 依存関係の更新
deps:
	@echo "依存関係を更新中..."
	cd src && go mod tidy
	cd src && go mod download

# Dockerイメージのビルド
docker-build:
	@echo "Dockerイメージをビルド中..."
	docker build -f Dockerfile -t misskey-backup-go ./src

# Dockerコンテナの起動
docker-run:
	@echo "Dockerコンテナを起動中..."
	docker compose up -d --build

# Dockerコンテナの停止
docker-stop:
	@echo "Dockerコンテナを停止中..."
	docker compose down

# ログの表示
logs:
	@echo "ログを表示中..."
	docker logs misskey-backup-go -f

# 手動バックアップの実行
backup:
	@echo "手動バックアップを実行中..."
	docker exec misskey-backup-go ./misskey-backup

# 開発環境のセットアップ
setup:
	@echo "開発環境をセットアップ中..."
	cp config/env.example config/.env
	@echo "設定ファイル config/.env を編集してください"

# テスト環境の実行
test-run:
	@echo "テスト環境を起動中..."
	docker compose -f compose.test.yaml up -d --build

# テスト環境の停止
test-stop:
	@echo "テスト環境を停止中..."
	docker compose -f compose.test.yaml down -v

# テスト環境のログ表示
test-logs:
	@echo "テスト環境のログを表示中..."
	docker compose -f compose.test.yaml logs -f

# 手動バックアップテスト
test-backup:
	@echo "手動バックアップテストを実行中..."
	docker exec misskey-backup-test ./misskey-backup
