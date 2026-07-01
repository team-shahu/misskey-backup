package config

import (
	"os"
	"testing"
)

func TestGetEnv(t *testing.T) {
	os.Unsetenv("X_TEST_STR")
	if v := getEnv("X_TEST_STR", "def"); v != "def" {
		t.Errorf("default: got %q", v)
	}
	t.Setenv("X_TEST_STR", "val")
	if v := getEnv("X_TEST_STR", "def"); v != "val" {
		t.Errorf("set: got %q", v)
	}
}

func TestGetEnvAsInt(t *testing.T) {
	os.Unsetenv("X_TEST_INT")
	if v := getEnvAsInt("X_TEST_INT", 42); v != 42 {
		t.Errorf("default: got %d", v)
	}
	t.Setenv("X_TEST_INT", "7")
	if v := getEnvAsInt("X_TEST_INT", 42); v != 7 {
		t.Errorf("set: got %d", v)
	}
	t.Setenv("X_TEST_INT", "notanint")
	if v := getEnvAsInt("X_TEST_INT", 42); v != 42 {
		t.Errorf("invalid should fall back: got %d", v)
	}
}

func TestGetEnvAsBool(t *testing.T) {
	os.Unsetenv("X_TEST_BOOL")
	if !getEnvAsBool("X_TEST_BOOL", true) {
		t.Error("default true expected")
	}
	t.Setenv("X_TEST_BOOL", "false")
	if getEnvAsBool("X_TEST_BOOL", true) {
		t.Error("set false expected")
	}
}

func TestLoadDefaults(t *testing.T) {
	os.Unsetenv("POSTGRES_PORT")
	os.Unsetenv("CRON_SCHEDULE")
	os.Unsetenv("POSTGRES_BACKUP_ENABLED")
	os.Unsetenv("REDIS_BACKUP_ENABLED")
	os.Unsetenv("REDIS_HOST")
	os.Unsetenv("REDIS_PORT")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PostgresPort != 5432 {
		t.Errorf("PostgresPort default: %d", cfg.PostgresPort)
	}
	if cfg.CronSchedule == "" {
		t.Error("CronSchedule should have a default")
	}
	// PostgresもRedisもデフォルト有効
	if !cfg.PostgresEnabled {
		t.Error("PostgresEnabled should default true")
	}
	if !cfg.RedisEnabled {
		t.Error("RedisEnabled should default true")
	}
	if cfg.RedisHost != "redis" {
		t.Errorf("RedisHost default: %q", cfg.RedisHost)
	}
	if cfg.RedisPort != 6379 {
		t.Errorf("RedisPort default: %d", cfg.RedisPort)
	}
}
