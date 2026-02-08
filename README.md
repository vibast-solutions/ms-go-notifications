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

## Health Check

- `GET /health` returns `{ "status": "ok" }`
