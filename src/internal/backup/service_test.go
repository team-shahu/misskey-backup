package backup

import (
	"strings"
	"testing"

	"misskey-backup/internal/config"
)

func TestBuildRedisCliArgs(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want []string
	}{
		{
			name: "TLSなし",
			cfg:  &config.Config{RedisHost: "redis", RedisPort: 6379},
			want: []string{"-h", "redis", "-p", "6379", "--rdb", "/tmp/redis.rdb"},
		},
		{
			name: "TLSあり",
			cfg:  &config.Config{RedisHost: "10.0.0.1", RedisPort: 6380, RedisTLS: true},
			want: []string{"-h", "10.0.0.1", "-p", "6380", "--tls", "--rdb", "/tmp/redis.rdb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{config: tt.cfg}
			got := s.buildRedisCliArgs("/tmp/redis.rdb")
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
