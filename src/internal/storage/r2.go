package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	mathrand "math/rand"
	"net"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"misskey-backup/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/sirupsen/logrus"
)

type R2Storage struct {
	client     *s3.Client
	bucketName string
	prefix     string
	config     *config.Config
}

// 大容量ファイル用デフォルトアップロードタイムアウト、rcloneに合わせ60分
const defaultUploadTimeout = 60 * time.Minute

// 署名付きURLの有効期限
const presignExpiry = 7 * 24 * time.Hour

// isRetryableError HTTPステータス・APIコード・ネットワークタイムアウトでリトライ可否を判定
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// HTTPステータスコード
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.HTTPStatusCode() {
		case 408, 429, 500, 502, 503, 504:
			return true
		}
	}

	// リトライ可能なAPIエラーコード
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "InternalError", "SlowDown", "RequestTimeout", "RequestTimeoutException",
			"ThrottlingException", "RequestThrottled", "ServiceUnavailable":
			return true
		}
	}

	// ネットワークタイムアウト
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
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
	jitter := delay * 0.25 * (mathrand.Float64()*2 - 1)
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

	// エンドポイントURLのホスト名からアカウントIDを抽出
	// 例: https://<accountID>.r2.cloudflarestorage.com
	parsed, err := url.Parse(cfg.R2Endpoint)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid R2 endpoint format: %s", cfg.R2Endpoint)
	}
	accountID := strings.TrimSuffix(parsed.Host, ".r2.cloudflarestorage.com")
	if accountID == "" || accountID == parsed.Host {
		return nil, fmt.Errorf("invalid R2 endpoint format: %s", cfg.R2Endpoint)
	}

	// Cloudflare R2公式ドキュメントに基づく設定
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.R2AccessKeyID,
			cfg.R2SecretAccessKey,
			"",
		)),
		awsconfig.WithRegion("auto"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// クライアント作成
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID))
	})

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

	// 1GB以上のファイルは単一アップロードを使用
	if fileSize > 1*1024*1024*1024 {
		logrus.Infof("Very large file detected (%.2f MB), using multipart upload", float64(fileSize)/1024/1024)
		return r.uploadMultipart(ctx, localPath, fullRemotePath, fileSize)
	}

	// 小さいファイルは通常のアップロード
	logrus.Infof("File size within single upload limit (%.2f MB), using simple upload", float64(fileSize)/1024/1024)
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

	return r.presignGetURL(ctx, fullRemotePath)
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

	// チャンクサイズを設定から取得（MB単位をバイト単位に変換）
	chunkSizeMB := r.config.ChunkSize
	if chunkSizeMB == 0 {
		chunkSizeMB = 10 // デフォルト値
	}
	chunkSize := int64(chunkSizeMB) * 1024 * 1024
	numParts := int((fileSize + chunkSize - 1) / chunkSize)

	logrus.Infof("Using chunk size: %d MB (%d parts)", chunkSizeMB, numParts)

	// 各パートを並列でアップロード
	var wg sync.WaitGroup
	completedParts := make([]types.CompletedPart, numParts)
	errors := make(chan error, numParts)

	// 並列度を制限（同時にアップロードするパート数）
	maxConcurrency := r.config.MaxConcurrency
	if maxConcurrency == 0 {
		maxConcurrency = 5 // デフォルト値
	}
	semaphore := make(chan struct{}, maxConcurrency)

	logrus.Infof("Starting parallel upload with %d concurrent parts", maxConcurrency)

	for partNumber := 1; partNumber <= numParts; partNumber++ {
		wg.Add(1)
		go func(partNum int) {
			defer wg.Done()

			// セマフォで並列度を制限
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			logrus.Infof("Uploading part %d/%d", partNum, numParts)

			// パートのサイズを計算
			start := int64(partNum-1) * chunkSize
			end := start + chunkSize
			if end > fileSize {
				end = fileSize
			}
			partSize := end - start

			// ファイルを開く（各ゴルーチンで独立したファイルハンドル）
			file, err := os.Open(localPath)
			if err != nil {
				errors <- fmt.Errorf("failed to open file for part %d: %w", partNum, err)
				return
			}
			defer file.Close()

			partNumberInt32 := int32(partNum)
			partSizeInt64 := partSize

			// パート単位でリトライ、再試行時は先頭へシーク
			var uploadPartResp *s3.UploadPartOutput
			err = r.retryWithBackoff(uploadCtx, func() error {
				if _, serr := file.Seek(start, 0); serr != nil {
					return fmt.Errorf("failed to seek: %w", serr)
				}
				var perr error
				uploadPartResp, perr = r.client.UploadPart(uploadCtx, &s3.UploadPartInput{
					Bucket:        aws.String(r.bucketName),
					Key:           aws.String(fullRemotePath),
					PartNumber:    &partNumberInt32,
					UploadId:      uploadID,
					Body:          io.LimitReader(file, partSize),
					ContentLength: &partSizeInt64,
				})
				return perr
			}, fmt.Sprintf("upload part %d/%d", partNum, numParts))
			if err != nil {
				errors <- fmt.Errorf("failed to upload part %d: %w", partNum, err)
				return
			}

			// 完了したパートを保存
			completedParts[partNum-1] = types.CompletedPart{
				ETag:       uploadPartResp.ETag,
				PartNumber: &partNumberInt32,
			}

			logrus.Infof("Completed part %d/%d", partNum, numParts)
		}(partNumber)
	}

	// すべてのゴルーチンの完了を待つ
	wg.Wait()
	close(errors)

	// エラーチェック
	for err := range errors {
		// uploadCtxが失効していても中止できるよう独立コンテキストで実行
		abortCtx, abortCancel := context.WithTimeout(context.Background(), 30*time.Second)
		r.client.AbortMultipartUpload(abortCtx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(r.bucketName),
			Key:      aws.String(fullRemotePath),
			UploadId: uploadID,
		})
		abortCancel()
		return "", fmt.Errorf("multipart upload failed: %w", err)
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

	return r.presignGetURL(ctx, fullRemotePath)
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

	var files []FileInfo
	var token *string
	for {
		var result *s3.ListObjectsV2Output
		err := r.retryWithBackoff(ctx, func() error {
			var err error
			result, err = r.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
				Bucket:            aws.String(r.bucketName),
				Prefix:            aws.String(fullPrefix),
				ContinuationToken: token,
			})
			return err
		}, "list objects from R2")
		if err != nil {
			return nil, fmt.Errorf("failed to list objects after retries: %w", err)
		}

		for _, obj := range result.Contents {
			// プレフィックスを除去してファイル名を取得
			fileName := *obj.Key
			if r.prefix != "" && strings.HasPrefix(fileName, r.prefix+"/") {
				fileName = fileName[len(r.prefix)+1:]
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

		if result.IsTruncated == nil || !*result.IsTruncated {
			break
		}
		token = result.NextContinuationToken
	}

	return files, nil
}

func (r *R2Storage) GetDownloadURL(ctx context.Context, remotePath string) (string, error) {
	fullRemotePath := path.Join(r.prefix, remotePath)
	return r.presignGetURL(ctx, fullRemotePath)
}

// presignGetURL GET用の署名付きURLを生成
func (r *R2Storage) presignGetURL(ctx context.Context, key string) (string, error) {
	presign := s3.NewPresignClient(r.client)
	req, err := presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucketName),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(presignExpiry))
	if err != nil {
		return "", fmt.Errorf("failed to presign download URL: %w", err)
	}
	return req.URL, nil
}
