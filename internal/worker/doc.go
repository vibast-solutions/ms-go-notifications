// Package worker holds the in-process send machinery: dispatcher (buffered
// channel + worker pool), processor (claim → build MIME → send → outcome),
// sweeper (re-dispatch queued/stale rows, reputation check) and the cleanup
// routines. MySQL is the queue — the claim UPDATE is the lock.
// Implementation arrives with VIB-9.
package worker
