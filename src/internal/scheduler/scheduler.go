package scheduler

import (
	"context"
	"fmt"
	"time"

	"misskey-backup/internal/backup"
	"misskey-backup/internal/config"
	"misskey-backup/internal/notification"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
)

type Scheduler struct {
	backupService       *backup.Service
	notificationService *notification.Service
	config              *config.Config
	cron                *cron.Cron
	entryID             cron.EntryID
}

func NewScheduler(backupService *backup.Service, notificationService *notification.Service, cfg *config.Config) *Scheduler {
	// タイムゾーン設定
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		logrus.Warnf("Failed to load timezone %s, using UTC: %v", cfg.Timezone, err)
		loc = time.UTC
	}

	c := cron.New(cron.WithLocation(loc))

	return &Scheduler{
		backupService:       backupService,
		notificationService: notificationService,
		config:              cfg,
		cron:                c,
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	// バックアップジョブの登録
	entryID, err := s.cron.AddFunc(s.config.CronSchedule, s.runBackup)
	if err != nil {
		return fmt.Errorf("failed to add cron job: %w", err)
	}
	s.entryID = entryID

	// cronスケジューラーの開始
	s.cron.Start()

	logrus.Infof("Scheduler started with schedule: %s (timezone: %s)",
		s.config.CronSchedule, s.config.Timezone)

	// 初回バックアップの実行（オプション）
	if s.shouldRunInitialBackup() {
		go func() {
			time.Sleep(5 * time.Second) // 少し待ってから実行
			s.runBackup()
		}()
	}

	// コンテキストのキャンセルを待機
	<-ctx.Done()
	return nil
}

func (s *Scheduler) Stop(ctx context.Context) error {
	// cronスケジューラーの停止
	stopCtx := s.cron.Stop()

	// 停止完了を待機
	select {
	case <-stopCtx.Done():
		logrus.Info("Scheduler stopped")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("context cancelled while stopping scheduler")
	}
}

func (s *Scheduler) runBackup() {
	startTime := time.Now()
	logrus.Info("Starting scheduled backup")

	// アップロードタイムアウトにダンプ/圧縮ぶんの余裕を加算(有効ターゲット逐次実行を包含)
	uploadTimeout := s.config.UploadTimeout
	if uploadTimeout <= 0 {
		uploadTimeout = 60
	}
	timeout := time.Duration(uploadTimeout)*time.Minute + 30*time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	results, err := s.backupService.CreateBackup(ctx)
	if err != nil {
		// 事前処理エラー(全体中断)
		duration := time.Since(startTime)
		logrus.Errorf("Backup failed: %v", err)

		if notifyErr := s.notificationService.NotifyBackupFailure(ctx, "バックアップ", err, duration); notifyErr != nil {
			logrus.Errorf("Failed to send failure notification: %v", notifyErr)
		}
		return
	}

	// ターゲットごとに個別通知
	for _, result := range results {
		if result.Success {
			if notifyErr := s.notificationService.NotifyBackupSuccess(ctx, result); notifyErr != nil {
				logrus.Errorf("Failed to send success notification: %v", notifyErr)
			}
		} else {
			if notifyErr := s.notificationService.NotifyBackupFailure(ctx, result.Target, result.Error, result.Duration); notifyErr != nil {
				logrus.Errorf("Failed to send failure notification: %v", notifyErr)
			}
		}
	}

	logrus.Infof("Scheduled backup finished in %v", time.Since(startTime))
}

func (s *Scheduler) shouldRunInitialBackup() bool {
	// 初回バックアップの実行条件を設定
	// 例: 起動後30分以内に次のスケジュールが来ない場合は実行
	now := time.Now()

	// 次の実行時刻を計算
	nextRun := s.cron.Entry(s.entryID).Next
	timeUntilNext := nextRun.Sub(now)

	// 次の実行まで30分以上ある場合は初回バックアップを実行
	return timeUntilNext > 30*time.Minute
}

// GetNextRun は次のバックアップ実行時刻を取得します
func (s *Scheduler) GetNextRun() time.Time {
	return s.cron.Entry(s.entryID).Next
}

// GetSchedule は現在のスケジュール設定を取得します
func (s *Scheduler) GetSchedule() string {
	return s.config.CronSchedule
}
