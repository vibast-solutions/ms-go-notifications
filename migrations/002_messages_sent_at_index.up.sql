-- Index messages.sent_at so the reputation circuit breaker's trailing-window
-- denominator (COUNT of non-suppressed recipients whose message m.sent_at >= ?)
-- can range-scan the window instead of full-scanning messages. The sweeper runs
-- this every 60s; without the index the cost grows with volume x retention.

ALTER TABLE messages ADD INDEX ix_messages_sent (sent_at);
