package lock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const releaseScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
end
return 0
`

type RedisLocker struct {
	client *redis.Client
	mu     sync.Mutex
	held   map[string]string
}

// NewRedisLocker constructs a Redis-based lock manager.
func NewRedisLocker(client *redis.Client) *RedisLocker {
	return &RedisLocker{
		client: client,
		held:   make(map[string]string),
	}
}

// Acquire obtains a Redis lock key with a TTL and stores the token locally.
func (l *RedisLocker) Acquire(ctx context.Context, key string, ttl time.Duration) error {
	l.mu.Lock()
	if _, exists := l.held[key]; exists {
		l.mu.Unlock()
		return ErrAlreadyHeld
	}
	l.mu.Unlock()

	token, err := randomToken(16)
	if err != nil {
		return err
	}

	ok, err := l.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotAcquired
	}

	l.mu.Lock()
	l.held[key] = token
	l.mu.Unlock()
	return nil
}

// Release frees a Redis lock key if this process owns it.
func (l *RedisLocker) Release(ctx context.Context, key string) error {
	l.mu.Lock()
	token, ok := l.held[key]
	if ok {
		delete(l.held, key)
	}
	l.mu.Unlock()

	if !ok {
		return nil
	}

	return l.client.Eval(ctx, releaseScript, []string{key}, token).Err()
}

// randomToken creates a hex token for Redis lock ownership.
func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
