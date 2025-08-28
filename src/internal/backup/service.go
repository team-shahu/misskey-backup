package backup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"misskey-backup/internal/config"
	"misskey-backup/internal/storage"

	"github.com/sirupsen/logrus"
)

type Service struct {
	config  *config.Config
	storage storage.Storage
}

type BackupResult struct {
	Success     bool
	FileName    string
	FileSize    int64
	Duration    time.Duration
	Error       error
	DownloadURL string
}

func NewService(cfg *config.Config) (*Service, error) {
	storageService, err := storage.NewR2Storage(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create R2 storage: %w", err)
	}

	return &Service{
		config:  cfg,
		storage: storageService,
	}, nil
}

func (s *Service) CreateBackup(ctx context.Context) (*BackupResult, error) {
	startTime := time.Now()
	result := &BackupResult{}

	// バックアップディレクトリの作成
	if err := os.MkdirAll(s.config.BackupDir, 0755); err != nil {
		// ディレクトリが既に存在する場合はエラーを無視
		if !os.IsExist(err) {
			return nil, fmt.Errorf("failed to create backup directory: %w", err)
		}
	}

	// バックアップファイル名の生成
	timestamp := time.Now().Format("2006-01-02_15-04")
	backupFileName := fmt.Sprintf("%s_%s.dump", s.config.PostgresDB, timestamp)
	backupFilePath := filepath.Join(s.config.BackupDir, backupFileName)
	compressedFilePath := backupFilePath + ".zst"

	defer func() {
		// 一時ファイルの削除
		os.Remove(backupFilePath)
		os.Remove(compressedFilePath)
	}()

	// PostgreSQLバックアップの実行
	if err := s.createPostgresBackup(backupFilePath); err != nil {
		result.Success = false
		result.Error = fmt.Errorf("failed to create PostgreSQL backup: %w", err)
		return result, result.Error
	}

	// ファイルの圧縮
	if err := s.compressFile(backupFilePath, compressedFilePath); err != nil {
		result.Success = false
		result.Error = fmt.Errorf("failed to compress backup file: %w", err)
		return result, result.Error
	}

	// ファイルサイズの取得
	fileInfo, err := os.Stat(compressedFilePath)
	if err != nil {
		result.Success = false
		result.Error = fmt.Errorf("failed to get file info: %w", err)
		return result, result.Error
	}

	// ストレージへのアップロード
	downloadURL, err := s.storage.Upload(ctx, compressedFilePath, backupFileName+".zst")
	if err != nil {
		result.Success = false
		result.Error = fmt.Errorf("failed to upload to storage: %w", err)
		return result, result.Error
	}

	// 古いバックアップの削除
	if err := s.cleanupOldBackups(ctx); err != nil {
		logrus.Warnf("Failed to cleanup old backups: %v", err)
	}

	result.Success = true
	result.FileName = backupFileName + ".zst"
	result.FileSize = fileInfo.Size()
	result.Duration = time.Since(startTime)
	result.DownloadURL = downloadURL

	logrus.Infof("Backup completed successfully: %s (%.2f MB, %v)",
		result.FileName, float64(result.FileSize)/1024/1024, result.Duration)

	return result, nil
}

func (s *Service) createPostgresBackup(filePath string) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		s.config.PostgresHost, s.config.PostgresPort, s.config.PostgresUser,
		s.config.PostgresPassword, s.config.PostgresDB)

	cmd := exec.Command("pg_dump", "-Fc", "-f", filePath, dsn)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	logrus.Infof("Creating PostgreSQL backup: %s", filePath)
	return cmd.Run()
}

func (s *Service) compressFile(inputPath, outputPath string) error {
	cmd := exec.Command("zstd", "-f", "-"+fmt.Sprintf("%d", s.config.CompressionLevel), inputPath, "-o", outputPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	logrus.Infof("Compressing backup file: %s", outputPath)
	return cmd.Run()
}

func (s *Service) cleanupOldBackups(ctx context.Context) error {
	// 古いバックアップファイルの削除
	cutoffDate := time.Now().AddDate(0, 0, -s.config.BackupRetention)

	files, err := s.storage.List(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	for _, file := range files {
		if file.ModTime.Before(cutoffDate) {
			if err := s.storage.Delete(ctx, file.Name); err != nil {
				logrus.Warnf("Failed to delete old backup %s: %v", file.Name, err)
			} else {
				logrus.Infof("Deleted old backup: %s", file.Name)
			}
		}
	}

	return nil
}
