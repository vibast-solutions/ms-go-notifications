# ms-go-notifications

Vibast notifications microservice: accepts email send requests over HTTP, queues them in MySQL, and delivers them through a pluggable provider (AWS SES in production, a log provider in development).

One Go binary, one MySQL database. There is no external queue — message rows in MySQL are the queue, drained by an in-process worker pool and a sweeper; a claim `UPDATE` acts as the lock so multiple instances are safe.

## Requirements

- Go 1.26+
- MySQL 8 (or Docker for the bundled compose file)

## Commands

The binary ships as cobra subcommands:

| Command | Purpose |
|---------|---------|
| `notifications serve` | HTTP API + in-process workers (`--no-worker` for API-only replicas) |
| `notifications migrate` | Apply embedded database migrations |

`apikey` (key management) and `cleanup` (attachment/history retention, cron) arrive in upcoming iterations.

## Development

```bash
make build      # build into build/notifications
make test       # unit tests
make lint       # golangci-lint
make e2e        # start MySQL in Docker, run integration tests, tear down
make migrate    # apply migrations using DATABASE_DSN from the environment/.env
make run        # run the HTTP server locally
```

Configuration is environment-based; copy `.env.dist` to `.env` and fill in values. Required: `DATABASE_DSN` and `ALLOWED_DOMAINS`; set `EMAIL_PROVIDER=log` to print emails to stdout instead of sending.

## Docker

```bash
docker build -t ms-go-notifications .
docker compose -f docker-compose.e2e.yml up -d --wait mysql-e2e   # local MySQL for tests
```

## API

- `GET /healthz` — liveness + DB reachability (`200 {"status":"ok","db":"ok"}` / `503`)
- `/v1/*` — versioned API (responses carry `X-API-Version: 1`); endpoints land in upcoming iterations

Errors use a stable envelope: `{"error": "<code>", "message": "<text>", "fields": {…}}`.
