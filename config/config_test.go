package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setRequired sets the minimum environment for Load to succeed.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_DSN", "user:pass@tcp(127.0.0.1:3306)/notifications")
	t.Setenv("ALLOWED_DOMAINS", "vibast.ro")
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, 25, cfg.DBMaxOpenConns)
	assert.Equal(t, 10, cfg.DBMaxIdleConns)
	assert.Equal(t, []string{"vibast.ro"}, cfg.AllowedDomains)
	assert.Equal(t, ProviderLog, cfg.EmailProvider)
	assert.Equal(t, 4, cfg.WorkerConcurrency)
	assert.Equal(t, 1024, cfg.DispatchBuffer)
	assert.Equal(t, 10*time.Second, cfg.SweepInterval)
	assert.Equal(t, 5*time.Minute, cfg.StaleLockAfter)
	assert.Equal(t, 5, cfg.SendMaxAttempts)
	assert.Equal(t, 5*time.Second, cfg.SendBackoffBase)
	assert.Equal(t, float64(10), cfg.ProviderMaxSendRate)
	assert.Equal(t, 50, cfg.MaxRecipients)
	assert.Equal(t, 10485760, cfg.MaxPayloadBytes)
	assert.Equal(t, 0.04, cfg.ReputationBounceThreshold)
	assert.Equal(t, 0.0008, cfg.ReputationComplaintThreshold)
	assert.Equal(t, 50, cfg.ReputationMinSample)
	assert.Empty(t, cfg.APIV1Sunset)
	assert.False(t, cfg.SwaggerEnabled)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "json", cfg.LogFormat)
}

func TestLoadMissingRequired(t *testing.T) {
	t.Setenv("DATABASE_DSN", "")
	t.Setenv("ALLOWED_DOMAINS", "")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_DSN")
	assert.Contains(t, err.Error(), "ALLOWED_DOMAINS")
}

func TestLoadProviderValidation(t *testing.T) {
	t.Run("unknown provider rejected", func(t *testing.T) {
		setRequired(t)
		t.Setenv("EMAIL_PROVIDER", "smtp")

		_, err := Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "EMAIL_PROVIDER")
	})

	t.Run("ses requires AWS_REGION", func(t *testing.T) {
		setRequired(t)
		t.Setenv("EMAIL_PROVIDER", "ses")
		t.Setenv("AWS_REGION", "")

		_, err := Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "AWS_REGION")
	})

	t.Run("ses with AWS_REGION accepted", func(t *testing.T) {
		setRequired(t)
		t.Setenv("EMAIL_PROVIDER", "ses")
		t.Setenv("AWS_REGION", "eu-central-1")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, ProviderSES, cfg.EmailProvider)
		assert.Equal(t, "eu-central-1", cfg.AWSRegion)
	})
}

func TestLoadInvalidValues(t *testing.T) {
	setRequired(t)
	t.Setenv("WORKER_CONCURRENCY", "many")
	t.Setenv("SWEEP_INTERVAL", "soon")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WORKER_CONCURRENCY")
	assert.Contains(t, err.Error(), "SWEEP_INTERVAL")
}

func TestLoadListParsing(t *testing.T) {
	setRequired(t)
	t.Setenv("ALLOWED_DOMAINS", " Vibast.ro , example.com ,")
	t.Setenv("SNS_ALLOWED_TOPIC_ARNS", "arn:aws:sns:eu-central-1:1:a,arn:aws:sns:eu-central-1:1:b")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"vibast.ro", "example.com"}, cfg.AllowedDomains)
	require.Len(t, cfg.SNSAllowedTopicARNs, 2)
	assert.True(t, strings.HasPrefix(cfg.SNSAllowedTopicARNs[0], "arn:aws:sns:"))
}
