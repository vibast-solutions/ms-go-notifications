package config

import (
	"testing"
	"time"
)

func TestLoadMissingMandatoryEnv(t *testing.T) {
	t.Setenv("SES_SOURCE_EMAIL", "")
	t.Setenv("MYSQL_DSN", "")
	t.Setenv("REDIS_ADDR", "")

	cfg, err := Load()
	if err == nil {
		t.Fatalf("expected error, got nil (cfg=%v)", cfg)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("SES_SOURCE_EMAIL", "noreply@example.com")
	t.Setenv("EMAIL_PROVIDER", "noop")
	t.Setenv("AWS_REGION", "")
	t.Setenv("MYSQL_DSN", "user:pass@tcp(localhost:3306)/notifications")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HTTP_HOST", "")
	t.Setenv("HTTP_PORT", "")
	t.Setenv("GRPC_HOST", "")
	t.Setenv("GRPC_PORT", "")
	t.Setenv("MYSQL_MAX_OPEN_CONNS", "")
	t.Setenv("MYSQL_MAX_IDLE_CONNS", "")
	t.Setenv("MYSQL_CONN_MAX_LIFETIME_MINUTES", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "")
	t.Setenv("AUTH_SERVICE_GRPC_ADDR", "")
	t.Setenv("APP_SERVICE_NAME", "")
	t.Setenv("APP_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.HTTP.Host != "0.0.0.0" || cfg.HTTP.Port != "8080" {
		t.Fatalf("unexpected HTTP defaults: %+v", cfg)
	}
	if cfg.GRPC.Host != "0.0.0.0" || cfg.GRPC.Port != "9090" {
		t.Fatalf("unexpected gRPC defaults: %+v", cfg)
	}
	if cfg.MySQL.MaxOpenConns != 10 || cfg.MySQL.MaxIdleConns != 5 {
		t.Fatalf("unexpected MySQL pool defaults: %+v", cfg)
	}
	if cfg.MySQL.ConnMaxLifetime != 30*time.Minute {
		t.Fatalf("unexpected MySQL max life default: %v", cfg.MySQL.ConnMaxLifetime)
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("expected LOG_LEVEL default 'info', got %q", cfg.Log.Level)
	}
	if cfg.InternalEndpoints.AuthGRPCAddr != "localhost:9090" {
		t.Fatalf("expected AUTH_SERVICE_GRPC_ADDR default, got %q", cfg.InternalEndpoints.AuthGRPCAddr)
	}
	if cfg.App.ServiceName != "notifications-service" {
		t.Fatalf("expected APP_SERVICE_NAME default, got %q", cfg.App.ServiceName)
	}
	if cfg.App.APIKey != "" {
		t.Fatalf("expected APP_API_KEY default empty, got %q", cfg.App.APIKey)
	}
	if cfg.EmailProviders.Provider != "noop" {
		t.Fatalf("unexpected EMAIL_PROVIDER: %q", cfg.EmailProviders.Provider)
	}
	if cfg.EmailProviders.AWS.SourceEmail != "noreply@example.com" {
		t.Fatalf("unexpected SES_SOURCE_EMAIL: %q", cfg.EmailProviders.AWS.SourceEmail)
	}
}

func TestLoadCustomValues(t *testing.T) {
	t.Setenv("SES_SOURCE_EMAIL", "noreply@example.com")
	t.Setenv("EMAIL_PROVIDER", "ses")
	t.Setenv("AWS_REGION", "eu-west-1")
	t.Setenv("MYSQL_DSN", "dsn")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("HTTP_HOST", "127.0.0.1")
	t.Setenv("HTTP_PORT", "8081")
	t.Setenv("GRPC_HOST", "127.0.0.2")
	t.Setenv("GRPC_PORT", "9091")
	t.Setenv("MYSQL_MAX_OPEN_CONNS", "42")
	t.Setenv("MYSQL_MAX_IDLE_CONNS", "12")
	t.Setenv("MYSQL_CONN_MAX_LIFETIME_MINUTES", "17")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("REDIS_PASSWORD", "secret")
	t.Setenv("REDIS_DB", "4")
	t.Setenv("AUTH_SERVICE_GRPC_ADDR", "auth:9090")
	t.Setenv("APP_SERVICE_NAME", "notifications-service")
	t.Setenv("APP_API_KEY", "notifications-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.HTTP.Host != "127.0.0.1" || cfg.HTTP.Port != "8081" {
		t.Fatalf("unexpected HTTP config: %+v", cfg)
	}
	if cfg.GRPC.Host != "127.0.0.2" || cfg.GRPC.Port != "9091" {
		t.Fatalf("unexpected gRPC config: %+v", cfg)
	}
	if cfg.MySQL.MaxOpenConns != 42 || cfg.MySQL.MaxIdleConns != 12 {
		t.Fatalf("unexpected MySQL pool config: %+v", cfg)
	}
	if cfg.MySQL.ConnMaxLifetime != 17*time.Minute {
		t.Fatalf("unexpected MySQL max life: %v", cfg.MySQL.ConnMaxLifetime)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("unexpected LOG_LEVEL: %q", cfg.Log.Level)
	}
	if cfg.Redis.Password != "secret" || cfg.Redis.DB != 4 {
		t.Fatalf("unexpected redis config: %+v", cfg)
	}
	if cfg.InternalEndpoints.AuthGRPCAddr != "auth:9090" {
		t.Fatalf("unexpected AUTH_SERVICE_GRPC_ADDR: %q", cfg.InternalEndpoints.AuthGRPCAddr)
	}
	if cfg.App.ServiceName != "notifications-service" {
		t.Fatalf("unexpected APP_SERVICE_NAME: %q", cfg.App.ServiceName)
	}
	if cfg.App.APIKey != "notifications-key" {
		t.Fatalf("unexpected APP_API_KEY: %q", cfg.App.APIKey)
	}
	if cfg.EmailProviders.Provider != "ses" {
		t.Fatalf("unexpected EMAIL_PROVIDER: %q", cfg.EmailProviders.Provider)
	}
	if cfg.EmailProviders.AWS.Region != "eu-west-1" {
		t.Fatalf("unexpected AWS_REGION: %q", cfg.EmailProviders.AWS.Region)
	}
}

func TestGetIntAndDurationFallback(t *testing.T) {
	t.Setenv("BROKEN_INT", "x")
	t.Setenv("BROKEN_MIN", "y")

	if got := getIntEnv("BROKEN_INT", 7); got != 7 {
		t.Fatalf("expected fallback int 7, got %d", got)
	}
	if got := getDurationEnv("BROKEN_MIN", 3*time.Minute); got != 3*time.Minute {
		t.Fatalf("expected fallback duration 3m, got %v", got)
	}
}
