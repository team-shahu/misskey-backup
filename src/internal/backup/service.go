package backup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"misskey-backup/internal/config"
	"misskey-backup/internal/storage"

	"github.com/sirupsen/logrus"
)

type Service struct {
	config  *config.Config
	storage storage.Storage
	encKey  []byte
	hmacKey []byte
}

type BackupResult struct {
	Success     bool
	FileName    string
	FileSize    int64
	Duration    time.Duration
	Error       error
	DownloadURL string
}

type downloadProgress struct {
	total        int64
	downloaded   int64
	lastLogged   int
	lastBytesLog int64
	lastLogTime  time.Time
}

func newDownloadProgress(total int64) *downloadProgress {
	return &downloadProgress{
		total:       total,
		lastLogTime: time.Now(),
	}
}

func (p *downloadProgress) Write(b []byte) (int, error) {
	n := len(b)
	p.downloaded += int64(n)

	now := time.Now()

	if p.total > 0 {
		percent := int(float64(p.downloaded) * 100 / float64(p.total))
		if percent >= p.lastLogged+5 {
			p.lastLogged = percent
			logrus.Infof("Downloading... %d%% (%0.1f / %0.1f MB)", percent, float64(p.downloaded)/1024/1024, float64(p.total)/1024/1024)
		}
	} else {
		if p.downloaded-p.lastBytesLog >= 10*1024*1024 || now.Sub(p.lastLogTime) >= 15*time.Second {
			p.lastBytesLog = p.downloaded
			p.lastLogTime = now
			logrus.Infof("Downloading... %0.1f MB", float64(p.downloaded)/1024/1024)
		}
	}

	return n, nil
}

func NewService(cfg *config.Config, restoreOnly bool) (*Service, error) {
	var storageService storage.Storage
	var err error

	if !restoreOnly {
		storageService, err = storage.NewR2Storage(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create R2 storage: %w", err)
		}
	}

	encKey, hmacKey, err := storage.DeriveEncryptionKeys(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare encryption keys: %w", err)
	}

	return &Service{
		config:  cfg,
		storage: storageService,
		encKey:  encKey,
		hmacKey: hmacKey,
	}, nil
}

func (s *Service) CreateBackup(ctx context.Context) (*BackupResult, error) {
	if s.storage == nil {
		return nil, fmt.Errorf("storage is not initialized")
	}

	startTime := time.Now()
	result := &BackupResult{}

	// バックアップディレクトリの作成
	if err := s.ensureBackupDir(); err != nil {
		return nil, err
	}

	// バックアップファイル名の生成
	timestamp := time.Now().Format("2006-01-02_15-04")
	backupFileName := fmt.Sprintf("%s_%s.dump", s.config.PostgresDB, timestamp)
	backupFilePath := filepath.Join(s.config.BackupDir, backupFileName)
	compressedFilePath := backupFilePath + ".zst"
	encryptedFilePath := compressedFilePath + ".enc"

	defer func() {
		// 一時ファイルの削除
		os.Remove(backupFilePath)
		os.Remove(compressedFilePath)
		os.Remove(encryptedFilePath)
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

	// キーがある場合は暗号化
	if s.config.EncryptionKey != "" {
		if err := storage.EncryptFile(compressedFilePath, encryptedFilePath, s.encKey, s.hmacKey); err != nil {
			result.Success = false
			result.Error = fmt.Errorf("failed to encrypt backup file: %w", err)
			return result, result.Error
		}
	}

	// 利用するファイルパスを確定
	useFilePath := compressedFilePath
	if s.config.EncryptionKey != "" {
		useFilePath = encryptedFilePath
	}

	// ファイルサイズの取得（暗号化後）
	fileInfo, err := os.Stat(useFilePath)
	if err != nil {
		result.Success = false
		result.Error = fmt.Errorf("failed to get file info: %w", err)
		return result, result.Error
	}

	// ストレージへのアップロード
	downloadURL, err := s.storage.Upload(ctx, encryptedFilePath, backupFileName+".zst.enc")
	if err != nil {
		result.Success = false
		result.Error = fmt.Errorf("failed to upload to storage: %w", err)
		return result, result.Error
	}

	// 古いバックアップの削除
	if err := s.cleanupOldBackups(ctx); err != nil {
		logrus.Warnf("Failed to cleanup old backups: %v", err)
	}

	// 暗号化の有無で拡張子を決定
	extension := ".zst"
	if s.config.EncryptionKey != "" {
		extension = ".zst.enc"
	}

	result.Success = true
	result.FileName = backupFileName + extension
	result.FileSize = fileInfo.Size()
	result.Duration = time.Since(startTime)
	result.DownloadURL = downloadURL

	logrus.Infof("Backup completed successfully: %s (%.2f MB, %v)",
		result.FileName, float64(result.FileSize)/1024/1024, result.Duration)

	return result, nil
}

func (s *Service) ensureBackupDir() error {
	if err := os.MkdirAll(s.config.BackupDir, 0755); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("failed to create backup directory: %w", err)
		}
	}
	return nil
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

// decompressFile zstdで解凍
func (s *Service) decompressFile(inputPath, outputPath string) error {
	cmd := exec.Command("zstd", "-d", "-f", inputPath, "-o", outputPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	logrus.Infof("Extracting backup file: %s", outputPath)
	return cmd.Run()
}

// RetrieveBackupFromURL 共有URLから暗号化済みバックアップを取得して復号・解凍
func (s *Service) RetrieveBackupFromURL(ctx context.Context, downloadURL string) (string, error) {
	if downloadURL == "" {
		return "", fmt.Errorf("download URL is empty")
	}

	//　restoreディレクトリの作成
	if err := os.MkdirAll("restore", 0755); err != nil {
		if !os.IsExist(err) {
			return "", fmt.Errorf("failed to create restore directory: %w", err)
		}
	}

	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return "", fmt.Errorf("invalid download URL: %w", err)
	}

	fileName := path.Base(parsed.Path)
	if fileName == "" || fileName == "." || fileName == "/" {
		return "", fmt.Errorf("download URL does not include file name")
	}

	encryptedPath := filepath.Join("restore", fileName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build HTTP request: %w", err)
	}

	logrus.Infof("Downloading backup file: %s", downloadURL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download backup: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status: %s", resp.Status)
	}

	out, err := os.Create(encryptedPath)
	if err != nil {
		return "", fmt.Errorf("failed to create local file: %w", err)
	}

	progress := newDownloadProgress(resp.ContentLength)
	reader := io.TeeReader(resp.Body, progress)

	if resp.ContentLength > 0 {
		logrus.Infof("Download size: %.2f MB", float64(resp.ContentLength)/1024/1024)
	}

	if _, err := io.Copy(out, reader); err != nil {
		out.Close()
		os.Remove(encryptedPath)
		return "", fmt.Errorf("failed to save downloaded file: %w", err)
	}

	if err := out.Close(); err != nil {
		os.Remove(encryptedPath)
		return "", fmt.Errorf("failed to close downloaded file: %w", err)
	}

	logrus.Infof("Download completed: %s (%.2f MB)", encryptedPath, float64(progress.downloaded)/1024/1024)

	return s.processEncryptedBackup(encryptedPath)
}

// processEncryptedBackup 暗号化済みファイルを復号→解凍してダンプを返す
func (s *Service) processEncryptedBackup(encryptedPath string) (string, error) {
	decryptedZstPath := strings.TrimSuffix(encryptedPath, ".enc")
	restoreDumpPath := strings.TrimSuffix(decryptedZstPath, ".zst")

	defer os.Remove(encryptedPath)

	if err := storage.DecryptFile(encryptedPath, decryptedZstPath, s.encKey, s.hmacKey); err != nil {
		return "", fmt.Errorf("failed to decrypt backup: %w", err)
	}
	defer os.Remove(decryptedZstPath)

	if err := s.decompressFile(decryptedZstPath, restoreDumpPath); err != nil {
		return "", fmt.Errorf("failed to decompress backup: %w", err)
	}

	logrus.Infof("Restored backup file: %s", restoreDumpPath)
	return restoreDumpPath, nil
}
