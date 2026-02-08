package lock

import (
	"context"
	"errors"
	"time"
)

var ErrAlreadyHeld = errors.New("lock already held by this process")
var ErrNotAcquired = errors.New("lock not acquired")

// Locker abstracts distributed locking implementations.
type Locker interface {
	// Acquire attempts to lock a key for the given TTL.
	Acquire(ctx context.Context, key string, ttl time.Duration) error
	// Release frees the lock for the given key.
	Release(ctx context.Context, key string) error
}
