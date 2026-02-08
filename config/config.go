package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPHost string
	HTTPPort string
	GRPCHost string
	GRPCPort string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	return &Config{
		HTTPHost: getEnv("HTTP_HOST", "0.0.0.0"),
		HTTPPort: getEnv("HTTP_PORT", "8080"),
		GRPCHost: getEnv("GRPC_HOST", "0.0.0.0"),
		GRPCPort: getEnv("GRPC_PORT", "9090"),
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
