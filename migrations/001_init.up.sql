-- Full initial schema. MySQL 8, InnoDB, utf8mb4. IDs are ULIDs stored as
-- CHAR(26); timestamps are DATETIME(3) in UTC. MySQL is the only state store
-- (it is also the queue). Foreign keys are declared only where cheap
-- (api_key_domains); message child tables rely on the cleanup command.

CREATE TABLE api_keys (
  id              CHAR(26) PRIMARY KEY,
  name            VARCHAR(100) NOT NULL,
  key_prefix      CHAR(8)  NOT NULL,             -- the "xxxxxxxx" in ntf_xxxxxxxx_…  (lookup)
  key_hash        CHAR(64) NOT NULL,             -- sha256 hex of full plaintext key
  scopes          JSON     NOT NULL,             -- ["send"] | ["admin"] | ["send","admin"]
  status          ENUM('active','revoked') NOT NULL DEFAULT 'active',
  created_at      DATETIME(3) NOT NULL,
  last_used_at    DATETIME(3) NULL,
  revoked_at      DATETIME(3) NULL,
  UNIQUE KEY uq_api_keys_prefix (key_prefix)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE api_key_domains (               -- a send key has >= 1 domain; no wildcard
  api_key_id  CHAR(26) NOT NULL,
  domain      VARCHAR(255) NOT NULL,          -- lower-case, must be in ALLOWED_DOMAINS at insert time
  created_at  DATETIME(3) NOT NULL,
  PRIMARY KEY (api_key_id, domain),
  KEY ix_api_key_domains_domain (domain),
  CONSTRAINT fk_akd_key FOREIGN KEY (api_key_id) REFERENCES api_keys(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE messages (
  id                  CHAR(26) PRIMARY KEY,
  api_key_id          CHAR(26) NOT NULL,
  channel             ENUM('email') NOT NULL DEFAULT 'email',
  status              ENUM('queued','sending','sent','failed','suppressed') NOT NULL,
  provider            VARCHAR(32) NULL,          -- set on first attempt
  provider_message_id VARCHAR(255) NULL,
  from_email          VARCHAR(320) NOT NULL,
  from_name           VARCHAR(255) NULL,
  from_domain         VARCHAR(255) NOT NULL,     -- denormalised for filtering/stats
  reply_to            JSON NULL,                 -- [{email,name}]
  subject             VARCHAR(998) NOT NULL,
  text_body           MEDIUMTEXT NULL,
  html_body           MEDIUMTEXT NULL,
  headers             JSON NULL,
  tags                JSON NULL,
  metadata            JSON NULL,
  idempotency_key     VARCHAR(128) NULL,
  attempts            INT NOT NULL DEFAULT 0,
  last_error          TEXT NULL,
  next_attempt_at     DATETIME(3) NULL,
  locked_at           DATETIME(3) NULL,          -- set when claimed (status=sending)
  locked_by           VARCHAR(128) NULL,         -- hostname:pid of the claiming process
  recipient_count     INT NOT NULL,
  created_at          DATETIME(3) NOT NULL,
  updated_at          DATETIME(3) NOT NULL,
  sent_at             DATETIME(3) NULL,
  finished_at         DATETIME(3) NULL,          -- terminal status reached (used by cleanup)
  UNIQUE KEY uq_messages_idem (api_key_id, idempotency_key),
  KEY ix_messages_due (status, next_attempt_at, created_at),   -- sweeper: queued & due
  KEY ix_messages_locked (status, locked_at),                  -- sweeper: stale sending
  KEY ix_messages_created (created_at),
  KEY ix_messages_domain_created (from_domain, created_at),
  KEY ix_messages_provider_msg (provider_message_id),
  KEY ix_messages_api_key_created (api_key_id, created_at),
  KEY ix_messages_finished (finished_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE message_recipients (
  id            CHAR(26) PRIMARY KEY,
  message_id    CHAR(26) NOT NULL,
  type          ENUM('to','cc','bcc') NOT NULL,
  email         VARCHAR(320) NOT NULL,
  name          VARCHAR(255) NULL,
  status        ENUM('pending','sent','delivered','bounced','complained','delayed','suppressed') NOT NULL,
  bounce_type   VARCHAR(32) NULL,   -- Permanent | Transient
  bounce_subtype VARCHAR(64) NULL,
  diagnostic    TEXT NULL,          -- SMTP diagnostic code / complaint feedback type
  last_event_at DATETIME(3) NULL,
  KEY ix_recipients_message (message_id),
  KEY ix_recipients_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE message_attachments (
  id           CHAR(26) PRIMARY KEY,
  message_id   CHAR(26) NOT NULL,
  filename     VARCHAR(255) NOT NULL,
  content_type VARCHAR(255) NOT NULL,
  size_bytes   INT NOT NULL,
  content      MEDIUMBLOB NULL,     -- NULL once purged by `cleanup --target attachments`
  purged_at    DATETIME(3) NULL,
  KEY ix_attachments_message (message_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE message_events (
  id                CHAR(26) PRIMARY KEY,
  message_id        CHAR(26) NOT NULL,
  recipient_email   VARCHAR(320) NULL,
  type              VARCHAR(32) NOT NULL,   -- queued|attempt|failed|suppressed|delivery|bounce|complaint|reject|delivery_delay|send|open|click|retry_requested|other
  provider_event_id VARCHAR(255) NULL,      -- SNS MessageId (dedupe)
  payload           JSON NULL,              -- attempt outcome / raw provider payload
  occurred_at       DATETIME(3) NOT NULL,
  created_at        DATETIME(3) NOT NULL,
  UNIQUE KEY uq_events_provider (provider_event_id, recipient_email),
  KEY ix_events_message (message_id, occurred_at),
  KEY ix_events_type_occurred (type, occurred_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE suppressions (
  email             VARCHAR(320) PRIMARY KEY,
  reason            ENUM('hard_bounce','complaint','manual') NOT NULL,
  source_message_id CHAR(26) NULL,
  note              VARCHAR(500) NULL,
  created_at        DATETIME(3) NOT NULL,
  expires_at        DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE settings (                       -- small runtime state, e.g. the pause flag
  `key`       VARCHAR(64) PRIMARY KEY,        -- 'sending_paused'
  value       JSON NOT NULL,                  -- {"reason":"…","since":"…","by":"auto|<apiKeyName>"}
  updated_at  DATETIME(3) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
