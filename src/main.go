package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"misskey-backup/internal/backup"
	"misskey-backup/internal/config"
	"misskey-backup/internal/notification"
	"misskey-backup/internal/scheduler"

	"github.com/sirupsen/logrus"
)

func main() {
	// ログ設定
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logrus.SetLevel(logrus.InfoLevel)

	// 設定読み込み
	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}

	// バックアップサービス初期化
	backupService, err := backup.NewService(cfg)
	if err != nil {
		logrus.Fatalf("Failed to create backup service: %v", err)
	}

	// 通知サービス初期化
	notificationService := notification.NewService(cfg)

	// スケジューラー初期化
	scheduler := scheduler.NewScheduler(backupService, notificationService, cfg)

	// コンテキスト作成
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// シグナルハンドリング
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// スケジューラー開始
	go func() {
		if err := scheduler.Start(ctx); err != nil {
			logrus.Errorf("Scheduler error: %v", err)
		}
	}()

	logrus.Info("Misskey backup service started")

	// シグナル待機
	<-sigChan
	logrus.Info("Shutting down...")

	// グレースフルシャットダウン
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := scheduler.Stop(shutdownCtx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	logrus.Info("Service stopped")
}
