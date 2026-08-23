// Package worker holds the in-process send machinery: the dispatcher (bounded,
// buffered worker pool), the processor (claim → build MIME → send → record
// outcome), and the sweeper (re-dispatch queued/stale rows, reputation hook).
// MySQL is the queue — the claim UPDATE is the lock, so multiple instances are
// safe. Retention (`cleanup`) is a separate cron command in cmd/notifications.
package worker
