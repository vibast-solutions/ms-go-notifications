# Notifications Microservice

`github.com/vibast-solutions/ms-go-notifications`

Notifications microservice providing delivery of email/SMS/push notifications via HTTP and gRPC interfaces.

## Requirements

- Go 1.25+

## Build

```bash
# Download dependencies
go mod tidy

# Build native binary
make build

# Cross-compile for Linux
make build-linux-arm64
make build-linux-amd64

# Cross-compile for macOS
make build-darwin-arm64
make build-darwin-amd64

# Build all targets
make build-all

# Clean build artifacts
make clean
```

## Run

```bash
# Run directly
go run main.go serve

# Or run the built binary
./build/notifications-service serve
```

The service starts:
- HTTP server on 0.0.0.0:8080
- gRPC server on 0.0.0.0:9090

## Configuration

Set environment variables or use defaults:

| Variable | Default | Description |
|----------|---------|-------------|
| HTTP_HOST | 0.0.0.0 | HTTP server bind address |
| HTTP_PORT | 8080 | HTTP server port |
| GRPC_HOST | 0.0.0.0 | gRPC server bind address |
| GRPC_PORT | 9090 | gRPC server port |
| AWS_REGION | (required for ses) | AWS region for SES |
| SES_SOURCE_EMAIL | (required) | Verified sender email for SES |
| EMAIL_PROVIDER | ses | Email provider: `ses` or `noop` |
| MYSQL_MAX_OPEN_CONNS | 10 | Max open DB connections |
| MYSQL_MAX_IDLE_CONNS | 5 | Max idle DB connections |
| MYSQL_CONN_MAX_LIFETIME_MINUTES | 30 | Max connection lifetime in minutes |

## Health Check

- `GET /health` returns `{ "status": "ok" }`

## Email Send

- `POST /email/send/raw` with JSON body `{"request_id":"uuid","recipient":"user@example.com","subject":"Hello","content":"Body text"}` sends an HTML email body using SES.
- Validation: `request_id` is required.
- Validation: `request_id` must be unique (idempotency); duplicates return 400.
- Validation: `recipient` must be a valid email address.
- Validation: `subject` must be at least 4 characters.
- Validation: `content` must be at least 11 characters.

## gRPC

Generate protobuf/grpc files:

```bash
./scripts/gen_proto.sh
```

Service:
`NotificationsService.SendRawEmail` with `request_id`, `recipient`, `subject`, `content`.
Response includes `success` and `error_message`.
