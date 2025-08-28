package storage

import (
	"context"
	"time"
)

// FileInfo ストレージ内のファイル情報
type FileInfo struct {
	Name    string
	Size    int64
	ModTime time.Time
}

// Storage ストレージサービスインターフェース
type Storage interface {
	// Upload ファイルをストレージにアップロード
	Upload(ctx context.Context, localPath, remotePath string) (string, error)

	// Download ファイルをストレージからダウンロード
	Download(ctx context.Context, remotePath, localPath string) error

	// Delete ファイルをストレージから削除
	Delete(ctx context.Context, remotePath string) error

	// List ストレージ内のファイル一覧を取得
	List(ctx context.Context, prefix string) ([]FileInfo, error)

	// GetDownloadURL ファイルのダウンロードURLを取得
	GetDownloadURL(ctx context.Context, remotePath string) (string, error)
}
