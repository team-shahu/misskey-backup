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
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
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

	// エンドポイントからアカウントIDを抽出
	// 例: https://a8e8211c674c2b00f3a8996b65b56447.r2.cloudflarestorage.com
	// から a8e8211c674c2b00f3a8996b65b56447 を抽出
	endpointURL := cfg.R2Endpoint
	accountID := ""
	if len(endpointURL) > 0 {
		// https:// を除去
		if len(endpointURL) > 8 && endpointURL[:8] == "https://" {
			accountID = endpointURL[8:]
		}
		// .r2.cloudflarestorage.com を除去
		if len(accountID) > 25 && accountID[len(accountID)-25:] == ".r2.cloudflarestorage.com" {
			accountID = accountID[:len(accountID)-25]
		}
	}

	if accountID == "" {
		return nil, fmt.Errorf("invalid R2 endpoint format: %s", cfg.R2Endpoint)
	}

	// R2エンドポイントリゾルバー
	r2Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL: fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID),
		}, nil
	})

	// AWS SDK設定
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithEndpointResolverWithOptions(r2Resolver),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.R2AccessKeyID,
			cfg.R2SecretAccessKey,
			"",
		)),
		awsconfig.WithRegion("apac"),
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

	// 100MB以上の場合はマルチパートアップロードを使用
	if fileSize > 100*1024*1024 {
		logrus.Infof("Large file detected (%.2f MB), using multipart upload", float64(fileSize)/1024/1024)
		return r.uploadMultipart(ctx, localPath, fullRemotePath, fileSize)
	}

	// 小さいファイルは通常のアップロード
	return r.uploadSimple(ctx, localPath, fullRemotePath)
}

// uploadSimple handles simple file uploads
func (r *R2Storage) uploadSimple(ctx context.Context, localPath, fullRemotePath string) (string, error) {
	// アップロード用のコンテキストにタイムアウトを設定
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

		logrus.Infof("Starting simple upload with timeout: %v", timeout)

		input := &s3.PutObjectInput{
			Bucket: aws.String(r.bucketName),
			Key:    aws.String(fullRemotePath),
			Body:   file,
		}

		_, err = r.client.PutObject(uploadCtx, input)
		if err != nil {
			logrus.Errorf("PutObject failed: %v", err)
			return err
		}

		logrus.Infof("Simple upload completed successfully")
		return nil
	}, "simple upload to R2")

	if err != nil {
		return "", fmt.Errorf("failed to upload file after retries: %w", err)
	}

	logrus.Infof("Uploaded %s to R2: %s", localPath, fullRemotePath)

	// ダウンロードURLを生成
	downloadURL := fmt.Sprintf("%s/%s/%s", r.config.R2Endpoint, r.bucketName, fullRemotePath)
	return downloadURL, nil
}

// uploadMultipart handles multipart upload for large files
func (r *R2Storage) uploadMultipart(ctx context.Context, localPath, fullRemotePath string, fileSize int64) (string, error) {
	timeout := time.Duration(r.config.UploadTimeout) * time.Minute
	if timeout == 0 {
		timeout = defaultUploadTimeout
	}
	uploadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logrus.Infof("Starting multipart upload with timeout: %v", timeout)

	// マルチパートアップロードの開始
	createResp, err := r.client.CreateMultipartUpload(uploadCtx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(r.bucketName),
		Key:    aws.String(fullRemotePath),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create multipart upload: %w", err)
	}

	uploadID := createResp.UploadId
	logrus.Infof("Created multipart upload with ID: %s", *uploadID)

	// チャンクサイズ（100MB）
	// https://developers.cloudflare.com/r2/examples/rclone/#a-note-about-multipart-upload-part-sizes
	// なんかこのくらいならいけそう
	chunkSize := int64(100 * 1024 * 1024)
	numParts := int((fileSize + chunkSize - 1) / chunkSize)

	var completedParts []types.CompletedPart

	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// 各パートをアップロード
	for partNumber := 1; partNumber <= numParts; partNumber++ {
		logrus.Infof("Uploading part %d/%d", partNumber, numParts)

		// パートのサイズを計算
		start := int64(partNumber-1) * chunkSize
		end := start + chunkSize
		if end > fileSize {
			end = fileSize
		}
		partSize := end - start

		// ファイルの位置を設定
		_, err = file.Seek(start, 0)
		if err != nil {
			return "", fmt.Errorf("failed to seek file: %w", err)
		}

		// パートをアップロード
		partNumberInt32 := int32(partNumber)
		partSizeInt64 := partSize

		uploadPartResp, err := r.client.UploadPart(uploadCtx, &s3.UploadPartInput{
			Bucket:        aws.String(r.bucketName),
			Key:           aws.String(fullRemotePath),
			PartNumber:    &partNumberInt32,
			UploadId:      uploadID,
			Body:          io.LimitReader(file, partSize),
			ContentLength: &partSizeInt64,
		})
		if err != nil {
			// マルチパートアップロードを中止
			r.client.AbortMultipartUpload(uploadCtx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(r.bucketName),
				Key:      aws.String(fullRemotePath),
				UploadId: uploadID,
			})
			return "", fmt.Errorf("failed to upload part %d: %w", partNumber, err)
		}

		completedParts = append(completedParts, types.CompletedPart{
			ETag:       uploadPartResp.ETag,
			PartNumber: &partNumberInt32,
		})

		logrus.Infof("Completed part %d/%d", partNumber, numParts)
	}

	// マルチパートアップロードを完了
	_, err = r.client.CompleteMultipartUpload(uploadCtx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(r.bucketName),
		Key:             aws.String(fullRemotePath),
		UploadId:        uploadID,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completedParts},
	})
	if err != nil {
		return "", fmt.Errorf("failed to complete multipart upload: %w", err)
	}

	logrus.Infof("Completed multipart upload for file: %s", localPath)

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
