package lock

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisLockerAcquireRelease(t *testing.T) {
	t.Parallel()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	lockerA := NewRedisLocker(client)
	lockerB := NewRedisLocker(client)

	if err := lockerA.Acquire(context.Background(), "lock-key", time.Minute); err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	if err := lockerB.Acquire(context.Background(), "lock-key", time.Minute); err != ErrNotAcquired {
		t.Fatalf("expected ErrNotAcquired, got %v", err)
	}
	if err := lockerA.Release(context.Background(), "lock-key"); err != nil {
		t.Fatalf("Release A: %v", err)
	}
	if err := lockerB.Acquire(context.Background(), "lock-key", time.Minute); err != nil {
		t.Fatalf("Acquire B after release: %v", err)
	}
	if err := lockerB.Release(context.Background(), "lock-key"); err != nil {
		t.Fatalf("Release B: %v", err)
	}
}

func TestRedisLockerAlreadyHeld(t *testing.T) {
	t.Parallel()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	locker := NewRedisLocker(client)
	if err := locker.Acquire(context.Background(), "lock-key", time.Minute); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := locker.Acquire(context.Background(), "lock-key", time.Minute); err != ErrAlreadyHeld {
		t.Fatalf("expected ErrAlreadyHeld, got %v", err)
	}
}
