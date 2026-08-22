// Package config loads and validates all service configuration from
// environment variables (with a .env file honoured in development via
// godotenv). Load fails fast: a missing required variable or an invalid
// value aborts startup with an error listing every problem found.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Providers accepted in EMAIL_PROVIDER.
const (
	ProviderSES = "ses"
	ProviderLog = "log"
)

// Config holds every runtime setting of the service. See .env.dist for the
// full reference with defaults and comments.
type Config struct {
	HTTPAddr string

	DatabaseDSN    string
	DBMaxOpenConns int
	DBMaxIdleConns int

	AllowedDomains []string

	EmailProvider       string
	AWSRegion           string
	SESConfigurationSet string
	SNSAllowedTopicARNs []string

	WorkerConcurrency int
	DispatchBuffer    int
	SweepInterval     time.Duration
	StaleLockAfter    time.Duration
	SendMaxAttempts   int
	SendBackoffBase   time.Duration

	ProviderMaxSendRate float64

	MaxRecipients   int
	MaxPayloadBytes int

	ReputationBounceThreshold    float64
	ReputationComplaintThreshold float64
	ReputationMinSample          int

	APIV1Sunset string

	SwaggerEnabled bool
	LogLevel       string
	LogFormat      string
}

// Load reads the environment (and a .env file when present), applies
// defaults, and validates. It returns an error describing every missing or
// invalid variable at once.
func Load() (*Config, error) {
	// Best effort: absence of a .env file is the normal production case.
	_ = godotenv.Load()

	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	cfg := &Config{
		HTTPAddr:            envString("HTTP_ADDR", ":8080"),
		DatabaseDSN:         envString("DATABASE_DSN", ""),
		DBMaxOpenConns:      envInt("DB_MAX_OPEN_CONNS", 25, fail),
		DBMaxIdleConns:      envInt("DB_MAX_IDLE_CONNS", 10, fail),
		AllowedDomains:      envList("ALLOWED_DOMAINS", true),
		EmailProvider:       envString("EMAIL_PROVIDER", ProviderLog),
		AWSRegion:           envString("AWS_REGION", ""),
		SESConfigurationSet: envString("SES_CONFIGURATION_SET", ""),
		SNSAllowedTopicARNs: envList("SNS_ALLOWED_TOPIC_ARNS", false),
		WorkerConcurrency:   envInt("WORKER_CONCURRENCY", 4, fail),
		DispatchBuffer:      envInt("DISPATCH_BUFFER", 1024, fail),
		SweepInterval:       envDuration("SWEEP_INTERVAL", 10*time.Second, fail),
		StaleLockAfter:      envDuration("STALE_LOCK_AFTER", 5*time.Minute, fail),
		SendMaxAttempts:     envInt("SEND_MAX_ATTEMPTS", 5, fail),
		SendBackoffBase:     envDuration("SEND_BACKOFF_BASE", 5*time.Second, fail),
		ProviderMaxSendRate: envFloat("PROVIDER_MAX_SEND_RATE", 10, fail),
		MaxRecipients:       envInt("MAX_RECIPIENTS", 50, fail),
		MaxPayloadBytes:     envInt("MAX_PAYLOAD_BYTES", 10485760, fail),

		ReputationBounceThreshold:    envFloat("REPUTATION_BOUNCE_THRESHOLD", 0.04, fail),
		ReputationComplaintThreshold: envFloat("REPUTATION_COMPLAINT_THRESHOLD", 0.0008, fail),
		ReputationMinSample:          envInt("REPUTATION_MIN_SAMPLE", 50, fail),

		APIV1Sunset:    envString("API_V1_SUNSET", ""),
		SwaggerEnabled: envBool("SWAGGER_ENABLED", false, fail),
		LogLevel:       envString("LOG_LEVEL", "info"),
		LogFormat:      envString("LOG_FORMAT", "json"),
	}

	if cfg.DatabaseDSN == "" {
		fail("DATABASE_DSN is required")
	}
	if len(cfg.AllowedDomains) == 0 {
		fail("ALLOWED_DOMAINS is required (comma-separated sender domains)")
	}
	switch cfg.EmailProvider {
	case ProviderSES:
		if cfg.AWSRegion == "" {
			fail("AWS_REGION is required when EMAIL_PROVIDER=ses")
		}
	case ProviderLog:
	default:
		fail("EMAIL_PROVIDER must be one of: ses, log (got %q)", cfg.EmailProvider)
	}

	if len(problems) > 0 {
		return nil, errors.New("invalid configuration: " + strings.Join(problems, "; "))
	}
	return cfg, nil
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

// envList parses a comma-separated variable into trimmed, non-empty items.
// Domains are lower-cased; ARNs keep their case (the resource part is
// case-sensitive). Returns nil when unset or empty.
func envList(key string, lower bool) []string {
	raw := envString(key, "")
	if raw == "" {
		return nil
	}
	var items []string
	for _, part := range strings.Split(raw, ",") {
		item := strings.TrimSpace(part)
		if lower {
			item = strings.ToLower(item)
		}
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func envInt(key string, def int, fail func(string, ...any)) int {
	raw := envString(key, "")
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		fail("%s must be an integer (got %q)", key, raw)
		return def
	}
	return v
}

func envFloat(key string, def float64, fail func(string, ...any)) float64 {
	raw := envString(key, "")
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		fail("%s must be a number (got %q)", key, raw)
		return def
	}
	return v
}

func envDuration(key string, def time.Duration, fail func(string, ...any)) time.Duration {
	raw := envString(key, "")
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		fail("%s must be a duration like 10s or 5m (got %q)", key, raw)
		return def
	}
	return v
}

func envBool(key string, def bool, fail func(string, ...any)) bool {
	raw := envString(key, "")
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		fail("%s must be a boolean (got %q)", key, raw)
		return def
	}
	return v
}
