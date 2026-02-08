# Notifications Microservice - Claude Context

## Overview
Notifications microservice built with Go, intended to deliver email/SMS/push notifications via HTTP and gRPC interfaces.

## Technology Stack
- **Framework**: Echo (HTTP), gRPC
- **CLI**: Cobra
- **Configuration**: Environment-based with optional `.env` support

## Module
- **Path**: `github.com/vibast-solutions/ms-go-notifications`
- Importable by other Go modules via `go get github.com/vibast-solutions/ms-go-notifications`

## Directory Structure
```
notifications/
├── main.go                 # Entry point, calls cmd.Execute()
├── Makefile                # Build targets (native, linux, darwin — arm64/amd64)
├── cmd/
│   ├── root.go             # Cobra root command
│   ├── serve.go            # Starts HTTP (8080) + gRPC (9090) servers
│   └── version.go          # Version command (shows git tag + commit hash)
├── config/
│   └── config.go           # Environment-based configuration
├── app/                    # Application logic (to be implemented)
└── proto/                  # gRPC definitions (to be implemented)
```

## Configuration (Environment Variables)
- `HTTP_HOST` (default: 0.0.0.0)
- `HTTP_PORT` (default: 8080)
- `GRPC_HOST` (default: 0.0.0.0)
- `GRPC_PORT` (default: 9090)

## Build
- `make build` — native binary to `build/notifications-service`
- `make build-linux-arm64` — Linux ARM64 cross-compile
- `make build-linux-amd64` — Linux AMD64 cross-compile
- `make build-darwin-arm64` — macOS ARM64 (Apple Silicon) cross-compile
- `make build-darwin-amd64` — macOS AMD64 (Intel) cross-compile
- `make build-all` — all targets
- `make clean` — remove `build/` directory
- Version and commit hash are injected at build time via `-ldflags` (see `cmd/version.go`)
