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
| `notifications apikey` | Manage API keys (see below) |
| `notifications cleanup` | Retention maintenance, cron (see [Retention](#retention)) |

## API keys

Every API request is authorised by a bearer API key with scopes (`send`, `admin`). A `send` key owns an explicit list of sender domains — no wildcards — each of which must be in the service's `ALLOWED_DOMAINS`. Only the SHA-256 hash of a key is stored; the plaintext (`ntf_<prefix>_<secret>`) is printed exactly once at creation.

Keys are managed by the operator via the CLI (uses `DATABASE_DSN` and `ALLOWED_DOMAINS` from the environment/`.env`):

```bash
notifications apikey create --name backend --scopes send --domains example.com,mail.example.com
notifications apikey create --name ops --scopes admin
notifications apikey list
notifications apikey set-domains <id> --domains example.com
notifications apikey revoke <id>
```

Requests authenticate with `Authorization: Bearer ntf_…`; `GET /v1/whoami` echoes the key's id, name, scopes and domains (handy as an SDK smoke test). Revoked keys fail immediately on the instance that revoked them and within 30 seconds on others (auth cache TTL); `last_used_at` is updated at most once per minute per key.

## Processing

After the accept transaction commits, the message id is handed to an in-process **dispatcher** — a buffered channel drained by `WORKER_CONCURRENCY` sender goroutines. Each worker:

1. waits for a rate-limit token (`PROVIDER_MAX_SEND_RATE` msgs/s, per instance),
2. **claims** the row atomically — `UPDATE … SET status='sending', attempts=attempts+1 WHERE id=? AND status='queued' AND (next_attempt_at IS NULL OR next_attempt_at <= NOW(3))`; the claim is the lock, so with several instances exactly one send happens,
3. builds the raw MIME message and sends it through the provider,
4. records the outcome: `sent` (terminal), `queued` again with a back-off `next_attempt_at` on a retryable error (`min(SEND_BACKOFF_BASE × 2^(attempts-1), 15m)` ±20% jitter, up to `SEND_MAX_ATTEMPTS`), or `failed` on a permanent error / exhausted attempts.

A **sweeper** runs on start-up and every `SWEEP_INTERVAL`: it drains queued/due rows into the pool (recovering bursts, dropped hand-offs and anything left behind by a restart) and returns rows stuck in `sending` for longer than `STALE_LOCK_AFTER` back to `queued`. This makes MySQL the durable queue — the cost of a backlog is latency, never a lost message.

The pool is intentionally **bounded**: throughput scales with `WORKER_CONCURRENCY` and the number of instances (split `PROVIDER_MAX_SEND_RATE` between them), never by spawning a goroutine per email. Accepting a request is never throttled — every request becomes a row and a `202`; only concurrent sends are bounded. When `settings.sending_paused` is set (by the reputation breaker or an admin, in later iterations), workers idle and the sweeper stops dispatching, while new requests keep queuing. Graceful shutdown stops claiming and lets in-flight sends finish (≤ 30 s).

`serve --no-worker` runs the HTTP API without the pool/sweeper, for API-only replicas; a worker instance elsewhere drains the shared queue.

## Retention

`cleanup` is a cron command that keeps the database small. It runs in batches (`--batch`, default 1000) with a short pause between them, prints the totals, and exits non-zero on error. `--older-than` below `24h` is refused.

```bash
# Null attachment content of messages that finished before the cutoff (default 7 days)
notifications cleanup --target attachments --older-than 168h

# Delete whole messages (with recipients, events, attachments) created before the cutoff (default 30 days)
notifications cleanup --target history --older-than 720h
```

Suggested crontab (attachments daily, history weekly):

```cron
15 3 * * *  notifications cleanup --target attachments   # 03:15 every day
45 3 * * 0  notifications cleanup --target history        # 03:45 every Sunday
```

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

- `GET /healthz` — liveness + DB reachability (`200 {"status":"ok","db":"ok"}` / `503`), unauthenticated
- `GET /v1/whoami` — authenticated key's id/name/scopes/domains
- `/v1/*` — versioned API (responses carry `X-API-Version: 1`), bearer-authenticated; more endpoints land in upcoming iterations

Errors use a stable envelope: `{"error": "<code>", "message": "<text>", "fields": {…}}`.
