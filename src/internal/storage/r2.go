package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"

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
	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// プレフィックスを付けてリモートパスを構築
	fullRemotePath := path.Join(r.prefix, remotePath)

	_, err = r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(r.bucketName),
		Key:    aws.String(fullRemotePath),
		Body:   file,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file: %w", err)
	}

	logrus.Infof("Uploaded %s to R2: %s", localPath, fullRemotePath)

	// ダウンロードURLを生成
	downloadURL := fmt.Sprintf("%s/%s/%s", r.config.R2Endpoint, r.bucketName, fullRemotePath)
	return downloadURL, nil
}

func (r *R2Storage) Download(ctx context.Context, remotePath, localPath string) error {
	fullRemotePath := path.Join(r.prefix, remotePath)

	result, err := r.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucketName),
		Key:    aws.String(fullRemotePath),
	})
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
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

	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucketName),
		Key:    aws.String(fullRemotePath),
	})
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	logrus.Infof("Deleted %s from R2", fullRemotePath)
	return nil
}

func (r *R2Storage) List(ctx context.Context, prefix string) ([]FileInfo, error) {
	fullPrefix := path.Join(r.prefix, prefix)

	result, err := r.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(r.bucketName),
		Prefix: aws.String(fullPrefix),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
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
