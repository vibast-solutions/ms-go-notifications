# Notifications Service Deployment Guide

This document describes what `notifications` needs in development and production.

## 1. Runtime Topology

Processes:

- API process: `notifications-service serve`
- Worker process: `notifications-service consume emails <consumer_name>`

Protocols:

- HTTP + gRPC (API process)
- Redis stream consumer (worker process)

Default ports (API):

- HTTP: `8080` (configurable with `HTTP_PORT`)
- gRPC: `9090` (configurable with `GRPC_PORT`)

External dependencies:

- MySQL: required
- Redis: required
- AWS SES: required when `EMAIL_PROVIDER=ses`

Redis stream/group used:

- Stream: `notifications:email:send-raw`
- Consumer group: `email-consumers`

## 2. Environment Variables

Required:

- `MYSQL_DSN`
- `REDIS_ADDR`
- `SES_SOURCE_EMAIL`
- `AWS_REGION` (required unless `EMAIL_PROVIDER=noop`)

Optional (with defaults):

- `EMAIL_PROVIDER` (default `ses`, supported: `ses`, `noop`)
- `HTTP_HOST` (default `0.0.0.0`)
- `HTTP_PORT` (default `8080`)
- `GRPC_HOST` (default `0.0.0.0`)
- `GRPC_PORT` (default `9090`)
- `MYSQL_MAX_OPEN_CONNS` (default `10`)
- `MYSQL_MAX_IDLE_CONNS` (default `5`)
- `MYSQL_CONN_MAX_LIFETIME_MINUTES` (default `30`)
- `REDIS_PASSWORD` (default empty)
- `REDIS_DB` (default `0`)
- `LOG_LEVEL` (default `info`)

Example DSNs:

- `MYSQL_DSN=user:pass@tcp(mysql-host:3306)/notifications?parseTime=true`
- `REDIS_ADDR=redis-host:6379`

AWS auth:

- Use IAM role (recommended) or standard AWS env credentials (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, optional `AWS_SESSION_TOKEN`).

## 3. MySQL Requirements

Database:

- name: `notifications`

Tables and indexes expected by the service:

```sql
CREATE DATABASE IF NOT EXISTS notifications;
USE notifications;

CREATE TABLE email_history
(
    id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    request_id VARCHAR(64)                        NOT NULL,
    recipient  VARCHAR(255)                       NOT NULL,
    subject    VARCHAR(255)                       NOT NULL,
    content    TEXT                               NOT NULL,
    status     SMALLINT DEFAULT 0                 NOT NULL,
    retries    INT      DEFAULT 0                 NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT idx_email_history_request_id UNIQUE (request_id)
);

CREATE INDEX idx_email_history_created_at ON email_history (created_at);
CREATE INDEX idx_email_history_recipient ON email_history (recipient);
CREATE INDEX idx_email_history_status ON email_history (status);
```

## 4. Redis Requirements

- Redis 7.x or compatible.
- Persistence policy should match your durability target (AOF/RDB).
- Worker concurrency is controlled by number of consumer processes and unique `consumer_name` values.

## 5. Development Setup

Recommended local stack:

- MySQL 8.x
- Redis 7.x
- API process + at least one consumer process
- `EMAIL_PROVIDER=noop` if you do not want real SES delivery

Reference e2e compose:

- `/Users/stefan.balea/projects/microservices-ecosystem/notifications/e2e/docker-compose.yml`

## 6. Production Notes

- Run API and consumer as separate deploy units so each can scale independently.
- Keep SES sender and credentials in secrets/identity system, not in repo.
- Monitor Redis lag, pending entries, and consumer health.
- Use least-privilege DB user on `notifications` schema.
- Keep `EMAIL_PROVIDER=ses` in production unless intentionally disabling outbound email.
