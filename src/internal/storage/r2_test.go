package storage

import (
	"errors"
	"testing"

	"misskey-backup/internal/config"

	"github.com/aws/smithy-go"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic", errors.New("boom"), false},
		{"internal error", &smithy.GenericAPIError{Code: "InternalError"}, true},
		{"slow down", &smithy.GenericAPIError{Code: "SlowDown"}, true},
		{"access denied", &smithy.GenericAPIError{Code: "AccessDenied"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableError(tt.err); got != tt.want {
				t.Errorf("isRetryableError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
		wantErr  bool
	}{
		{"r2", "https://acc.r2.cloudflarestorage.com", "https://acc.r2.cloudflarestorage.com", false},
		{"r2 with path", "https://acc.r2.cloudflarestorage.com/bucket", "https://acc.r2.cloudflarestorage.com", false},
		{"http with port", "http://rustfs:9000", "http://rustfs:9000", false},
		{"aws s3", "https://s3.ap-northeast-1.amazonaws.com/", "https://s3.ap-northeast-1.amazonaws.com", false},
		{"no scheme", "acc.r2.cloudflarestorage.com", "", true},
		{"unsupported scheme", "ftp://example.com", "", true},
		{"empty host", "https://", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeEndpoint(tt.endpoint)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeEndpoint() err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("normalizeEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewR2StorageOptions(t *testing.T) {
	tests := []struct {
		name          string
		cfg           config.Config
		wantEndpoint  string
		wantRegion    string
		wantPathStyle bool
	}{
		{
			name:         "r2 defaults",
			cfg:          config.Config{R2Endpoint: "https://acc.r2.cloudflarestorage.com"},
			wantEndpoint: "https://acc.r2.cloudflarestorage.com",
			wantRegion:   "auto",
		},
		{
			name:          "s3 compatible",
			cfg:           config.Config{R2Endpoint: "http://rustfs:9000", R2Region: "us-east-1", R2ForcePathStyle: true},
			wantEndpoint:  "http://rustfs:9000",
			wantRegion:    "us-east-1",
			wantPathStyle: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			cfg.R2AccessKeyID = "key"
			cfg.R2SecretAccessKey = "secret"
			s, err := NewR2Storage(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			opts := s.client.Options()
			if opts.BaseEndpoint == nil || *opts.BaseEndpoint != tt.wantEndpoint {
				t.Errorf("BaseEndpoint = %v, want %q", opts.BaseEndpoint, tt.wantEndpoint)
			}
			if opts.Region != tt.wantRegion {
				t.Errorf("Region = %q, want %q", opts.Region, tt.wantRegion)
			}
			if opts.UsePathStyle != tt.wantPathStyle {
				t.Errorf("UsePathStyle = %v, want %v", opts.UsePathStyle, tt.wantPathStyle)
			}
		})
	}
}

func TestNewR2StorageIncomplete(t *testing.T) {
	if _, err := NewR2Storage(&config.Config{R2Endpoint: "http://rustfs:9000"}); err == nil {
		t.Error("expected error for missing credentials")
	}
}
