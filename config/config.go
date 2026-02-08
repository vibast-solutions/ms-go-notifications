package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPHost       string
	HTTPPort       string
	GRPCHost       string
	GRPCPort       string
	MySQLDSN       string
	MySQLMaxOpen   int
	MySQLMaxIdle   int
	MySQLMaxLife   time.Duration
	AWSRegion      string
	SESSourceEmail string
	EmailProvider  string
	RedisAddr      string
	RedisPassword  string
	RedisDB        int
}

// Load reads configuration from environment variables (and .env when present).
func Load() (*Config, error) {
	_ = godotenv.Load()

	sesSource := os.Getenv("SES_SOURCE_EMAIL")
	if sesSource == "" {
		return nil, errors.New("SES_SOURCE_EMAIL environment variable is required")
	}

	emailProvider := getEnv("EMAIL_PROVIDER", "ses")
	awsRegion := os.Getenv("AWS_REGION")
	if emailProvider != "noop" && awsRegion == "" {
		return nil, errors.New("AWS_REGION environment variable is required")
	}

	mysqlDSN := os.Getenv("MYSQL_DSN")
	if mysqlDSN == "" {
		return nil, errors.New("MYSQL_DSN environment variable is required")
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		return nil, errors.New("REDIS_ADDR environment variable is required")
	}

	return &Config{
		HTTPHost:       getEnv("HTTP_HOST", "0.0.0.0"),
		HTTPPort:       getEnv("HTTP_PORT", "8080"),
		GRPCHost:       getEnv("GRPC_HOST", "0.0.0.0"),
		GRPCPort:       getEnv("GRPC_PORT", "9090"),
		MySQLDSN:       mysqlDSN,
		MySQLMaxOpen:   getIntEnv("MYSQL_MAX_OPEN_CONNS", 10),
		MySQLMaxIdle:   getIntEnv("MYSQL_MAX_IDLE_CONNS", 5),
		MySQLMaxLife:   getDurationEnv("MYSQL_CONN_MAX_LIFETIME_MINUTES", 30*time.Minute),
		AWSRegion:      awsRegion,
		SESSourceEmail: sesSource,
		EmailProvider:  emailProvider,
		RedisAddr:      redisAddr,
		RedisPassword:  getEnv("REDIS_PASSWORD", ""),
		RedisDB:        getIntEnv("REDIS_DB", 0),
	}, nil
}

// getEnv returns the env value or the default if empty.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getIntEnv returns the int env value or the default if empty/invalid.
func getIntEnv(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return defaultValue
}

// getDurationEnv returns a minutes-based duration from env or the default.
func getDurationEnv(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if minutes, err := strconv.Atoi(value); err == nil {
			return time.Duration(minutes) * time.Minute
		}
	}
	return defaultValue
}
