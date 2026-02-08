package lock

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

type MySQLLocker struct {
	db    *sql.DB
	mu    sync.Mutex
	conns map[string]*sql.Conn
}

// NewMySQLLocker constructs a MySQL-based advisory lock manager.
func NewMySQLLocker(db *sql.DB) *MySQLLocker {
	return &MySQLLocker{
		db:    db,
		conns: make(map[string]*sql.Conn),
	}
}

// Acquire obtains a named MySQL advisory lock and holds a connection.
func (l *MySQLLocker) Acquire(ctx context.Context, key string, ttl time.Duration) error {
	l.mu.Lock()
	if _, exists := l.conns[key]; exists {
		l.mu.Unlock()
		return ErrAlreadyHeld
	}
	l.mu.Unlock()

	conn, err := l.db.Conn(ctx)
	if err != nil {
		return err
	}

	timeoutSeconds := int(ttl.Seconds())
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}

	var acquired int
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", key, timeoutSeconds).Scan(&acquired); err != nil {
		_ = conn.Close()
		return err
	}
	if acquired != 1 {
		_ = conn.Close()
		return ErrNotAcquired
	}

	l.mu.Lock()
	l.conns[key] = conn
	l.mu.Unlock()

	return nil
}

// Release frees a named MySQL advisory lock and closes its connection.
func (l *MySQLLocker) Release(ctx context.Context, key string) error {
	l.mu.Lock()
	conn, ok := l.conns[key]
	if ok {
		delete(l.conns, key)
	}
	l.mu.Unlock()

	if !ok {
		return nil
	}

	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", key); err != nil {
		return err
	}
	return nil
}
