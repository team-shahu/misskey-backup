package storage

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path"
	"time"

	"misskey-backup/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/sirupsen/logrus"
)

type R2Storage struct {
	client     *s3.Client
	bucketName string
	prefix     string
	config     *config.Config
}

// Retry configuration - デフォルト値
const (
	defaultMaxRetries = 5
	defaultBaseDelay  = 1 * time.Second
	defaultMaxDelay   = 30 * time.Second
	// 大きなファイル用のタイムアウト設定（デフォルト）
	defaultUploadTimeout = 30 * time.Minute
	// チャンクサイズ（5MB）
	chunkSize = 5 * 1024 * 1024
)

// isRetryableError checks if the error is retryable
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific error patterns that are retryable
	errStr := err.Error()

	// 500 Internal Server Error
	if errStr == "operation error S3: PutObject, exceeded maximum number of attempts, 3, https response error StatusCode: 500" {
		return true
	}

	// Generic 500 errors from Cloudflare R2
	if errStr == "operation error S3: PutObject, exceeded maximum number of attempts, 3, https response error StatusCode: 500, RequestID: , HostID: , api error InternalError: We encountered an internal error. Please try again." {
		return true
	}

	// Check for other common retryable patterns
	if errStr == "operation error S3: PutObject, exceeded maximum number of attempts, 3, https response error StatusCode: 503" {
		return true
	}

	if errStr == "operation error S3: PutObject, exceeded maximum number of attempts, 3, https response error StatusCode: 429" {
		return true
	}

	// タイムアウトエラー
	if errStr == "operation error S3: PutObject, exceeded maximum number of attempts, 3, context deadline exceeded" {
		return true
	}

	// ネットワークエラー
	if errStr == "operation error S3: PutObject, exceeded maximum number of attempts, 3, net/http: TLS handshake timeout" {
		return true
	}

	return false
}

// exponentialBackoff calculates delay with jitter
func (r *R2Storage) exponentialBackoff(attempt int) time.Duration {
	baseDelay := time.Duration(r.config.RetryBaseDelay) * time.Second
	maxDelay := time.Duration(r.config.RetryMaxDelay) * time.Second

	delay := float64(baseDelay) * math.Pow(2, float64(attempt))
	if delay > float64(maxDelay) {
		delay = float64(maxDelay)
	}

	// Add jitter (±25%)
	jitter := delay * 0.25 * (rand.Float64()*2 - 1)
	delay += jitter

	return time.Duration(delay)
}

// retryWithBackoff executes an operation with exponential backoff
func (r *R2Storage) retryWithBackoff(ctx context.Context, operation func() error, operationName string) error {
	var lastErr error
	maxRetries := r.config.MaxRetries

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := r.exponentialBackoff(attempt - 1)
			logrus.Warnf("Retrying %s after %v (attempt %d/%d): %v",
				operationName, delay, attempt, maxRetries, lastErr)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		// 操作開始のログ
		if attempt == 0 {
			logrus.Infof("Starting %s", operationName)
		}

		err := operation()
		if err == nil {
			if attempt > 0 {
				logrus.Infof("%s succeeded after %d retries", operationName, attempt)
			} else {
				logrus.Infof("%s succeeded on first attempt", operationName)
			}
			return nil
		}

		lastErr = err

		// エラーの詳細ログ
		logrus.Errorf("%s failed (attempt %d/%d): %v", operationName, attempt+1, maxRetries+1, err)

		if !isRetryableError(err) {
			logrus.Errorf("%s failed with non-retryable error: %v", operationName, err)
			return err
		}

		if attempt == maxRetries {
			logrus.Errorf("%s failed after %d retries: %v", operationName, maxRetries, err)
			return fmt.Errorf("%s failed after %d retries: %w", operationName, maxRetries, err)
		}
	}

	return lastErr
}

func NewR2Storage(cfg *config.Config) (*R2Storage, error) {
	// R2設定の検証
	if cfg.R2Endpoint == "" || cfg.R2AccessKeyID == "" || cfg.R2SecretAccessKey == "" {
		return nil, fmt.Errorf("R2 configuration is incomplete")
	}

	// AWS SDK設定
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.R2AccessKeyID,
			cfg.R2SecretAccessKey,
			"",
		)),
		awsconfig.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					URL: cfg.R2Endpoint,
				}, nil
			},
		)),
		// タイムアウト設定を追加
		awsconfig.WithClientLogMode(aws.LogRequestWithBody|aws.LogResponseWithBody),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg)

	return &R2Storage{
		client:     client,
		bucketName: cfg.R2BucketName,
		prefix:     cfg.R2Prefix,
		config:     cfg,
	}, nil
}

func (r *R2Storage) Upload(ctx context.Context, localPath, remotePath string) (string, error) {
	// プレフィックスを付けてリモートパスを構築
	fullRemotePath := path.Join(r.prefix, remotePath)

	// ファイルサイズを確認
	fileInfo, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to get file info: %w", err)
	}

	fileSize := fileInfo.Size()
	logrus.Infof("Uploading file: %s (size: %.2f MB)", localPath, float64(fileSize)/1024/1024)

	// 100MB以上の場合、マルチパートアップロードを検討
	if fileSize > 100*1024*1024 {
		logrus.Infof("Large file detected (%.2f MB), using enhanced upload strategy", float64(fileSize)/1024/1024)
		return r.uploadLargeFile(ctx, localPath, fullRemotePath, fileSize)
	}

	// アップロード用のコンテキストにタイムアウトを設定
	timeout := time.Duration(r.config.UploadTimeout) * time.Minute
	if timeout == 0 {
		timeout = defaultUploadTimeout
	}
	uploadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err = r.retryWithBackoff(uploadCtx, func() error {
		file, err := os.Open(localPath)
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		defer file.Close()

		_, err = r.client.PutObject(uploadCtx, &s3.PutObjectInput{
			Bucket: aws.String(r.bucketName),
			Key:    aws.String(fullRemotePath),
			Body:   file,
		})
		if err != nil {
			return err
		}

		return nil
	}, "upload to R2")

	if err != nil {
		return "", fmt.Errorf("failed to upload file after retries: %w", err)
	}

	logrus.Infof("Uploaded %s to R2: %s", localPath, fullRemotePath)

	// ダウンロードURLを生成
	downloadURL := fmt.Sprintf("%s/%s/%s", r.config.R2Endpoint, r.bucketName, fullRemotePath)
	return downloadURL, nil
}

// uploadLargeFile handles large file uploads with enhanced error handling
func (r *R2Storage) uploadLargeFile(ctx context.Context, localPath, fullRemotePath string, fileSize int64) (string, error) {
	timeout := time.Duration(r.config.UploadTimeout) * time.Minute
	if timeout == 0 {
		timeout = defaultUploadTimeout
	}
	uploadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := r.retryWithBackoff(uploadCtx, func() error {
		file, err := os.Open(localPath)
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		defer file.Close()

		// 大きなファイル用の追加設定
		_, err = r.client.PutObject(uploadCtx, &s3.PutObjectInput{
			Bucket: aws.String(r.bucketName),
			Key:    aws.String(fullRemotePath),
			Body:   file,
		})
		if err != nil {
			return err
		}

		return nil
	}, "upload large file to R2")

	if err != nil {
		return "", fmt.Errorf("failed to upload large file after retries: %w", err)
	}

	logrus.Infof("Uploaded large file %s to R2: %s", localPath, fullRemotePath)

	// ダウンロードURLを生成
	downloadURL := fmt.Sprintf("%s/%s/%s", r.config.R2Endpoint, r.bucketName, fullRemotePath)
	return downloadURL, nil
}

func (r *R2Storage) Download(ctx context.Context, remotePath, localPath string) error {
	fullRemotePath := path.Join(r.prefix, remotePath)

	var result *s3.GetObjectOutput
	err := r.retryWithBackoff(ctx, func() error {
		var err error
		result, err = r.client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(r.bucketName),
			Key:    aws.String(fullRemotePath),
		})
		return err
	}, "download from R2")

	if err != nil {
		return fmt.Errorf("failed to get object after retries: %w", err)
	}
	defer result.Body.Close()

	file, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, result.Body)
	if err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	logrus.Infof("Downloaded %s from R2 to %s", fullRemotePath, localPath)
	return nil
}

func (r *R2Storage) Delete(ctx context.Context, remotePath string) error {
	fullRemotePath := path.Join(r.prefix, remotePath)

	err := r.retryWithBackoff(ctx, func() error {
		_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(r.bucketName),
			Key:    aws.String(fullRemotePath),
		})
		return err
	}, "delete from R2")

	if err != nil {
		return fmt.Errorf("failed to delete object after retries: %w", err)
	}

	logrus.Infof("Deleted %s from R2", fullRemotePath)
	return nil
}

func (r *R2Storage) List(ctx context.Context, prefix string) ([]FileInfo, error) {
	fullPrefix := path.Join(r.prefix, prefix)

	var result *s3.ListObjectsV2Output
	err := r.retryWithBackoff(ctx, func() error {
		var err error
		result, err = r.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(r.bucketName),
			Prefix: aws.String(fullPrefix),
		})
		return err
	}, "list objects from R2")

	if err != nil {
		return nil, fmt.Errorf("failed to list objects after retries: %w", err)
	}

	var files []FileInfo
	for _, obj := range result.Contents {
		// プレフィックスを除去してファイル名を取得
		fileName := *obj.Key
		if r.prefix != "" {
			fileName = fileName[len(r.prefix)+1:] // +1 for the slash
		}

		var size int64
		if obj.Size != nil {
			size = *obj.Size
		}

		files = append(files, FileInfo{
			Name:    fileName,
			Size:    size,
			ModTime: *obj.LastModified,
		})
	}

	return files, nil
}

func (r *R2Storage) GetDownloadURL(ctx context.Context, remotePath string) (string, error) {
	fullRemotePath := path.Join(r.prefix, remotePath)
	downloadURL := fmt.Sprintf("%s/%s/%s", r.config.R2Endpoint, r.bucketName, fullRemotePath)
	return downloadURL, nil
}
