# Notifications Microservice - Claude Context

## Overview
Email notifications service with async delivery via Redis streams, AWS SES integration, distributed locking, and idempotent request handling.

## Technology Stack
- **Framework**: Echo (HTTP), gRPC
- **CLI**: Cobra
- **Database**: MySQL (email history tracking)
- **Queue**: Redis Streams (async email delivery)
- **Email Provider**: AWS SES v2 (pluggable, noop provider for dev/testing)
- **Locking**: Redis (primary) or MySQL advisory locks (pluggable)
- **Configuration**: Environment-based with optional `.env` support

## Module
- **Path**: `github.com/vibast-solutions/ms-go-notifications`

## Directory Structure
```
notifications/
├── main.go
├── Makefile
├── cmd/
│   ├── root.go             # Cobra root command
│   ├── serve.go            # HTTP + gRPC servers
│   ├── consume.go          # Email queue consumer command
│   ├── version.go          # Version command (ldflags)
│   └── logging.go          # Logrus configuration
├── config/
│   └── config.go           # Environment variable loading
├── app/
│   ├── controller/
│   │   └── email.go        # HTTP handler (POST /email/send/raw)
│   ├── service/
│   │   ├── email.go        # Email send orchestration
│   │   ├── errors.go       # Service-level sentinel errors
│   │   └── context.go      # Request ID context helpers
│   ├── repository/
│   │   └── email_history.go # MySQL CRUD for email history
│   ├── entity/
│   │   └── email_history.go # EmailHistory entity + status codes
│   ├── provider/
│   │   ├── provider.go     # EmailProvider interface
│   │   ├── ses.go          # AWS SES implementation
│   │   └── noop.go         # No-op stub provider
│   ├── preparer/
│   │   ├── preparer.go     # EmailPreparer interface + chain pattern
│   │   └── raw.go          # Raw MIME message builder
│   ├── queue/
│   │   ├── message.go      # EmailMessage + publisher/consumer interfaces
│   │   ├── producer.go     # Redis stream producer
│   │   └── consumer.go     # Redis consumer worker (consumer group)
│   ├── lock/
│   │   ├── locker.go       # Locker interface
│   │   ├── redis.go        # Redis distributed lock (SetNX + Lua release)
│   │   └── mysql.go        # MySQL advisory lock
│   ├── dto/
│   │   └── send_raw.go     # SendRawRequest DTO + validation
│   ├── grpc/
│   │   └── server.go       # gRPC handler
│   └── types/
│       ├── notifications.pb.go
│       └── notifications_grpc.pb.go
├── proto/
│   └── notifications.proto
└── scripts/
    └── gen_proto.sh
```

## Request Flow

**Sync (HTTP/gRPC):** Client → Validate → Create email history (idempotent via unique request_id) → Publish to Redis stream → Return 200.

**Async (Consumer worker):** Read from Redis stream → Acquire distributed lock → Update status to Processing → Prepare MIME message → Send via SES → Update status to Success → Ack message.

## CLI Commands
- `notifications serve` — Start HTTP + gRPC servers
- `notifications consume emails <consumer_name>` — Start email queue consumer worker

## Configuration (Environment Variables)
- `HTTP_HOST` / `HTTP_PORT` (default: 0.0.0.0:8080)
- `GRPC_HOST` / `GRPC_PORT` (default: 0.0.0.0:9090)
- `MYSQL_DSN` (required)
- `REDIS_ADDR` (required)
- `AWS_REGION` (required)
- `SES_SOURCE_EMAIL` (required)
- `EMAIL_PROVIDER` (default: ses, options: ses/noop)
- `LOG_LEVEL` (default: info)
- `MYSQL_MAX_OPEN_CONNS`, `MYSQL_MAX_IDLE_CONNS`, `MYSQL_CONN_MAX_LIFETIME_MINUTES`

## Email Status Codes
New(0) → Processing(1) → Success(10) | TemporaryFailure(40) | UnknownFailure(49) | PermanentFailure(50)

## Key Patterns
- Idempotency via unique constraint on `request_id` in email_history table
- At-least-once delivery via Redis consumer groups with explicit ACKs
- Distributed lock prevents concurrent processing of the same email
- Pluggable providers/lockers via interfaces

## Build
- `make build` — native binary to `build/notifications-service`
- `make build-all` — cross-compile for linux/darwin arm64/amd64
- Version and commit hash injected via `-ldflags`
