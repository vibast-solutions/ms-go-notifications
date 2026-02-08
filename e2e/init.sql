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
    CONSTRAINT idx_email_history_request_id
        UNIQUE (request_id)
);

CREATE INDEX idx_email_history_created_at
    ON email_history (created_at);

CREATE INDEX idx_email_history_recipient
    ON email_history (recipient);

CREATE INDEX idx_email_history_status
    ON email_history (status);
