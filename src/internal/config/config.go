package config

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	// PostgreSQL設定
	PostgresHost     string
	PostgresPort     int
	PostgresUser     string
	PostgresPassword string
	PostgresDB       string

	// バックアップ設定
	BackupDir        string
	BackupRetention  int
	CompressionLevel int

	// Cloudflare R2設定
	R2Endpoint        string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2BucketName      string
	R2Prefix          string
	R2BucketACL       string

	// リトライ設定
	MaxRetries     int
	RetryBaseDelay int // 秒単位
	RetryMaxDelay  int // 秒単位

	// アップロード設定
	UploadTimeout int // 分単位

	// デバッグ設定
	Debug bool

	// 通知設定
	Notification      bool
	DiscordWebhookURL string

	// スケジューラー設定
	CronSchedule string
	Timezone     string
}

func Load() (*Config, error) {
	// .envファイルを読み込み
	if err := godotenv.Load("config/.env"); err != nil {
		// .envファイルが存在しない場合は環境変数から読み込み
	}

	cfg := &Config{
		PostgresHost:      getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort:      getEnvAsInt("POSTGRES_PORT", 5432),
		PostgresUser:      getEnv("POSTGRES_USER", "postgres"),
		PostgresPassword:  getEnv("POSTGRES_PASSWORD", ""),
		PostgresDB:        getEnv("POSTGRES_DB", "misskey"),
		BackupDir:         getEnv("BACKUP_DIR", "/app/backups"),
		BackupRetention:   getEnvAsInt("BACKUP_RETENTION", 30),
		CompressionLevel:  getEnvAsInt("COMPRESSION_LEVEL", 3),
		R2Endpoint:        getEnv("BACKUP_ENDPOINT", ""),
		R2AccessKeyID:     getEnv("BACKUP_ACCESS_KEY_ID", ""),
		R2SecretAccessKey: getEnv("BACKUP_SECRET_ACCESS_KEY", ""),
		R2BucketName:      getEnv("R2_BUCKET_NAME", ""),
		R2Prefix:          getEnv("R2_PREFIX", ""),
		R2BucketACL:       getEnv("BACKUP_BUCKET_ACL", ""),
		MaxRetries:        getEnvAsInt("MAX_RETRIES", 5),
		RetryBaseDelay:    getEnvAsInt("RETRY_BASE_DELAY", 1),
		RetryMaxDelay:     getEnvAsInt("RETRY_MAX_DELAY", 30),
		UploadTimeout:     getEnvAsInt("UPLOAD_TIMEOUT", 120),
		Debug:             getEnvAsBool("DEBUG", false),
		Notification:      getEnvAsBool("NOTIFICATION", false),
		DiscordWebhookURL: getEnv("DISCORD_WEBHOOK_URL", ""),
		CronSchedule:      getEnv("CRON_SCHEDULE", "0 5,17 * * *"),
		Timezone:          getEnv("TZ", "Asia/Tokyo"),
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvAsBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}
