package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"misskey-backup/internal/backup"
	"misskey-backup/internal/config"

	"github.com/sirupsen/logrus"
)

type Service struct {
	config *config.Config
	client *http.Client
}

type DiscordEmbed struct {
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Color       int                 `json:"color,omitempty"`
	Fields      []DiscordEmbedField `json:"fields,omitempty"`
	Timestamp   string              `json:"timestamp,omitempty"`
}

type DiscordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type DiscordWebhook struct {
	Embeds []DiscordEmbed `json:"embeds"`
}

func NewService(cfg *config.Config) *Service {
	return &Service{
		config: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (s *Service) NotifyBackupSuccess(ctx context.Context, result *backup.BackupResult) error {
	if !s.config.Notification || s.config.DiscordWebhookURL == "" {
		return nil
	}

	embed := DiscordEmbed{
		Title:       "✅ バックアップが完了しました。",
		Description: "PostgreSQLのバックアップが正常に完了しました",
		Color:       5620992, // 緑色
		Timestamp:   time.Now().Format(time.RFC3339),
		Fields: []DiscordEmbedField{
			{
				Name:   ":file_folder: 保存先",
				Value:  result.FileName,
				Inline: true,
			},
			{
				Name:   ":timer: 実行時間",
				Value:  fmt.Sprintf("%.1fs", result.Duration.Seconds()),
				Inline: true,
			},
		},
	}

	// ダウンロードURLを追加
	if result.DownloadURL != "" {
		embed.Fields = append(embed.Fields, DiscordEmbedField{
			Name:   ":link: ダウンロードURL",
			Value:  result.DownloadURL,
			Inline: true,
		})
	}

	// ファイルサイズを追加
	fileSizeMB := float64(result.FileSize) / 1024 / 1024
	embed.Fields = append(embed.Fields, DiscordEmbedField{
		Name:   ":floppy_disk: ファイルサイズ",
		Value:  fmt.Sprintf("%.2f MB", fileSizeMB),
		Inline: true,
	})

	return s.sendDiscordWebhook(ctx, embed)
}

func (s *Service) NotifyBackupFailure(ctx context.Context, err error, duration time.Duration) error {
	if !s.config.Notification || s.config.DiscordWebhookURL == "" {
		return nil
	}

	embed := DiscordEmbed{
		Title:       "❌ バックアップに失敗しました。",
		Description: "PostgreSQLのバックアップが異常終了しました。ログを確認してください。",
		Color:       15548997, // 赤色
		Timestamp:   time.Now().Format(time.RFC3339),
		Fields: []DiscordEmbedField{
			{
				Name:   ":timer: 実行時間",
				Value:  fmt.Sprintf("%.1fs", duration.Seconds()),
				Inline: true,
			},
			{
				Name:   ":warning: エラー",
				Value:  err.Error(),
				Inline: false,
			},
		},
	}

	return s.sendDiscordWebhook(ctx, embed)
}

func (s *Service) sendDiscordWebhook(ctx context.Context, embed DiscordEmbed) error {
	webhook := DiscordWebhook{
		Embeds: []DiscordEmbed{embed},
	}

	jsonData, err := json.Marshal(webhook)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook data: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.config.DiscordWebhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("webhook request failed with status: %d", resp.StatusCode)
	}

	logrus.Infof("Discord notification sent successfully")
	return nil
}
