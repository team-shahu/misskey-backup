# ビルドステージ
FROM golang:1.25-alpine AS builder

# 必要なパッケージをインストール
RUN apk add --no-cache git ca-certificates tzdata

# 作業ディレクトリを設定
WORKDIR /app

# go.modとgo.sumをコピー
COPY src/go.mod src/go.sum ./

# 依存関係をダウンロード
RUN go mod download

# ソースコードをコピー
COPY src/ .

# バイナリをビルド
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o misskey-backup .

# 実行ステージ
FROM alpine:latest

# 必要なパッケージをインストール
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    postgresql-client \
    zstd \
    curl \
    && rm -rf /var/cache/apk/*

# 非rootユーザーを作成
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

# 作業ディレクトリを設定
WORKDIR /app

# ビルドステージからバイナリをコピー
COPY --from=builder /app/misskey-backup .

# 設定ファイル用ディレクトリを作成
RUN mkdir -p /app/config

# 権限を設定
RUN chown -R appuser:appgroup /app

# ユーザーを切り替え
USER appuser

# ヘルスチェック
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# ポートを公開（必要に応じて）
EXPOSE 8080

# アプリケーションを実行
CMD ["./misskey-backup"]
