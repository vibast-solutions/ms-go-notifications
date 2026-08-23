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

## Provider events (SES via SNS)

SES publishes per-message events (delivery, bounce, complaint, delivery delay, reject) to an SNS topic, which POSTs them to `POST /v1/webhooks/ses`. This closes the feedback loop: recipient statuses are updated and permanent bounces / complaints grow the suppression list so future sends skip those addresses.

The webhook has **no bearer auth** — the SNS message signature is the credential:

1. The SNS signature is verified against the message's signing certificate (`SignatureVersion` 1 = SHA1withRSA, 2 = SHA256withRSA). The certificate URL must be `https` on an `sns.<region>.amazonaws.com` host, and the certificate is cached after first download.
2. The message's `TopicArn` must be listed in `SNS_ALLOWED_TOPIC_ARNS` (comma-separated). An empty list rejects everything — set it before pointing SNS at the service.
3. `SubscriptionConfirmation` messages for an allowed topic are auto-confirmed (the service GETs the `SubscribeURL`). `Notification` messages are parsed and applied.

Responses: `403` on a bad signature or a disallowed topic, `400` on a malformed body, and `200` once a message is verified — including for an unknown message id (logged and dropped, so SNS does not retry a message the service can never match). Duplicate SNS deliveries are ignored via a unique index on `(provider_event_id, recipient_email)`. Transient bounces update the recipient status but never suppress.

### AWS setup

The SES configuration set, SNS topic, event destination and IAM permissions are provisioned outside this repo — see the **Notifications Technical Design, §7 infrastructure checklist** (tracked by ticket **VIB-13**). In short: create an SNS topic, attach it as the event destination of the SES configuration set named in `SES_CONFIGURATION_SET` (subscribing to at least Delivery, Bounce, Complaint, DeliveryDelay, Reject), subscribe the topic to `https://<host>/v1/webhooks/ses`, and add the topic ARN to `SNS_ALLOWED_TOPIC_ARNS`.

## Admin API

Operator endpoints live under `/v1/admin` and require an `admin`-scoped key. They back the admin app; a `send`-only key gets `403 forbidden`.

- `GET /v1/admin/messages` — paginated message history. Filters: `status`, `fromDomain`, `from` (sender contains), `to` (recipient contains), `subject` (contains), `apiKeyId`, `tag` (`key:value`), `createdFrom`/`createdTo` (RFC3339 or `YYYY-MM-DD`). `limit` ≤ 100 (default 25). Keyset pagination on `(createdAt, id)` descending: pass the returned `nextCursor` back as `cursor`. Cursors are stable under concurrent inserts. Response: `{ items: [summary], nextCursor }`.
- `GET /v1/admin/messages/{id}` — full detail for debugging: the consumer view plus bodies (`text`, `html`), `headers`, attachment metadata (`id, filename, contentType, sizeBytes, purged`), the owning `apiKey {id, name}`, `lockedAt`/`lockedBy` and full event payloads.
- `GET /v1/admin/messages/{id}/attachments/{attachmentId}` — streams the attachment with its `Content-Type` and a `Content-Disposition` filename. `410 gone` once the content has been purged by retention.
- `POST /v1/admin/messages/{id}/retry` — re-queues a **failed** message (event `retry_requested`, attributed to the calling key) and re-dispatches it → `202`. Any other status → `409 invalid_state`.
- `GET /v1/admin/stats` — dashboard numbers over a UTC window (`from`, `to`, default last 7 days; `fromDomain`; `granularity=day|hour`). Returns a zero-filled per-bucket series of `accepted / sent / failed / suppressed / delivered / bouncedHard / bouncedSoft / complained`, `totals`, the 24 h rolling `bounceRate`/`complaintRate`, the `sending` circuit-breaker state and live `backlog` gauges (`queued`, `sending`).
- `GET /v1/admin/sending` · `POST /v1/admin/sending/pause` · `POST /v1/admin/sending/resume` — reputation circuit-breaker state and manual control (see [Processing](#processing)).
- `GET /v1/admin/suppressions` (filters `email` contains, `reason`; cursor/limit) · `POST /v1/admin/suppressions {email, note?}` → `201` (reason `manual`, `409 conflict` if already suppressed) · `DELETE /v1/admin/suppressions/{email}` → `204` (`404` if absent).
- `GET /v1/admin/api-keys` (never the secret) · `POST /v1/admin/api-keys {name, scopes, domains}` → `201` with the plaintext key **once** (same validation as the CLI: `domains` required for the `send` scope, each in `ALLOWED_DOMAINS`) · `PUT /v1/admin/api-keys/{id}/domains {domains}` → `200` · `DELETE /v1/admin/api-keys/{id}` → `204`. A key cannot revoke itself (`400 invalid_request`).

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
- `POST /v1/webhooks/ses` — SES event webhook (SNS), authenticated by SNS signature (see [Provider events](#provider-events-ses-via-sns))
- `POST /v1/emails` · `GET /v1/emails/{id}` — accept an email and read its status/timeline (`send` scope)
- `/v1/admin/*` — operator API: message history/detail/attachments/retry, stats, sending control, suppressions and API-key management (`admin` scope; see [Admin API](#admin-api))
- `/v1/*` — versioned API (responses carry `X-API-Version: 1`), bearer-authenticated

Errors use a stable envelope: `{"error": "<code>", "message": "<text>", "fields": {…}}`.
