# ms-go-notifications — Claude Code Instructions

## What This Is

Notifications microservice for Vibast: accepts email send requests over HTTP, queues them in MySQL, and delivers them through a provider (AWS SES in production, a log provider in development). One Go binary, one MySQL database — nothing else.

**No Redis — MySQL is the queue.** Rows in `messages` are the queue items; an in-process dispatcher + worker pool sends them and a sweeper re-dispatches queued/stale rows. The claim `UPDATE … WHERE status='queued'` is the lock, so multiple instances are safe.

## Tech Stack

- Go 1.26, Echo v5, MySQL 8 (raw SQL, no ORM)
- cobra subcommands: `serve` (HTTP + in-process workers; `--no-worker` for API-only), `migrate`; `apikey` and `cleanup` arrive in later stories
- golang-migrate with migrations embedded via `embed.FS`
- logrus (JSON in prod), godotenv for local dev
- IDs are ULIDs stored as `CHAR(26)`; timestamps `DATETIME(3)` UTC

## Project Layout

```
cmd/notifications/     → cobra root + serve/migrate wiring (main.go, serve.go, server.go, migrate.go)
config/                → env parsing + validation (config.go)
app/entity/            → domain structs mirroring the schema
app/repository/        → SQL data access (one file per aggregate)
app/service/           → business logic, interfaces, domain errors
app/dto/v1/            → request/response DTOs for API v1 (Validate())
app/controller/v1/     → HTTP handlers for /v1 (thin)
app/middleware/        → request id, error handler, API version; auth in VIB-7
pkg/provider/          → EmailProvider interface; ses/log impls in VIB-9
pkg/sns/               → SNS envelope parsing + signature verification (later)
pkg/mime/              → raw MIME builder (later)
internal/worker/       → dispatcher, processor, sweeper, cleanup (VIB-9)
migrations/            → embedded SQL migrations (001_init.up.sql, …)
docs/                  → generated swagger (do not hand-edit)
e2e/                   → integration tests behind the e2e build tag
```

## Architecture Rules

- **Dependency flow**: controller → service → repository → entity (never reverse)
- **Services depend on interfaces** defined in `app/service/interfaces.go`, not concrete types
- **Controllers stay thin**: bind request, call service, return response
- **Repositories only do data access**: no business logic, parameterized SQL only
- **No global mutable state**: constructor-based dependency injection in `cmd/notifications`

## Key Patterns

### Handler Signature (Echo v5)
All handlers use `*echo.Context` (pointer, not interface):
```go
func (h *HealthController) Healthz(c *echo.Context) error {
```

### Error Handler Signature (Echo v5)
```go
func ErrorHandler(c *echo.Context, err error)  // params are (context, error) — NOT (error, context)
```

### Error Responses
All errors follow this envelope:
```json
{"error": "error_code", "message": "human message", "fields": {"field": "reason"}}
```

### DTO Validation
DTOs have `Validate() map[string]string` — returns nil on success, field→message map on failure.

### Config
`config.Load()` fails fast and reports every problem at once. Required: `DATABASE_DSN`, `ALLOWED_DOMAINS`; `EMAIL_PROVIDER` ∈ {ses, log}; `AWS_REGION` required when ses. See `.env.dist` for all variables.

## Migrations

- Numbered pairs under `migrations/`: `NNN_name.up.sql` + `NNN_name.down.sql`, applied by `notifications migrate` (embedded FS; golang-migrate keeps its `schema_migrations` table)
- Multi-statement files need `multiStatements=true` on the DSN — the migrate command appends it automatically
- Schema follows the data model page: ENUM columns for closed sets, `CHAR(26)` ULIDs, `DATETIME(3)`, InnoDB + utf8mb4

## Commands

```bash
make build      # go build -o build/notifications ./cmd/notifications
make test       # go test ./...
make lint       # golangci-lint run
make e2e        # docker compose MySQL + go test -tags=e2e ./e2e/
make migrate    # go run ./cmd/notifications migrate
```

## E2E Tests

- Live in `e2e/` behind `//go:build e2e` — never run with plain `go test ./...`
- `make e2e` starts only the MySQL container (`mysql-e2e`, port 13306), runs the tests, tears down
- New migrations/tables: extend the table list in `e2e/migrate_test.go`

## Do NOT

- **Never run the server on this VM** — build, tests, lint only; no `go run … serve`, no compose `--profile full`
- Add Redis or any queue/broker — MySQL is the only state store and the queue (decided 2026-08-17)
- Use an ORM — raw parameterized SQL only
- Put business logic in controllers or repositories
- Expose raw DB/driver errors in API responses
- Hand-edit files in `docs/` — they are generated
