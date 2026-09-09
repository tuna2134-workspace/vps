package operations

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	store, err := OpenBolt(t.TempDir() + "/ops.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewService(store)
}

// TestExecuteRunsExactlyOnce verifies a retried operation executes the work
// only once and returns the same success.
func TestExecuteRunsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	var calls int32
	fn := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}

	for i := 0; i < 3; i++ {
		res, err := svc.Execute(ctx, "op-1", "create", "vm-1", "hash-1", fn)
		if err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
		if !res.Succeeded() {
			t.Fatalf("execute %d: expected success, got %v", i, res.Error)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("work executed %d times, want 1", got)
	}
}

// TestExecutePersistenceAcrossStore verifies a succeeded operation is still
// idempotent after the store is reopened (agent restart).
func TestExecutePersistenceAcrossStore(t *testing.T) {
	path := t.TempDir() + "/ops.db"
	ctx := context.Background()

	store, err := OpenBolt(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	svc := NewService(store)

	var calls int32
	if _, err := svc.Execute(ctx, "op-1", "create", "vm-1", "hash-1", func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}); err != nil {
		t.Fatalf("first execute: %v", err)
	}

	// Simulate agent restart: close and reopen the same DB file.
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	store2, err := OpenBolt(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store2.Close()

	svc2 := NewService(store2)
	res, err := svc2.Execute(ctx, "op-1", "create", "vm-1", "hash-1", func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("replay after restart: %v", err)
	}
	if !res.Succeeded() {
		t.Fatalf("replay after restart: expected success, got %v", res.Error)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("work executed %d times after restart, want 1", got)
	}
}

// TestExecuteRejectsDifferentRequest verifies a same-id, different-hash retry
// is rejected instead of executed.
func TestExecuteRejectsDifferentRequest(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	if _, err := svc.Execute(ctx, "op-1", "create", "vm-1", "hash-A", func(ctx context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if _, err := svc.Execute(ctx, "op-1", "create", "vm-1", "hash-B", func(ctx context.Context) error {
		return errors.New("should not run")
	}); err == nil {
		t.Fatal("expected error for conflicting request hash")
	}
}

// TestExecuteNeverDoubleRunsConcurrent verifies two concurrent calls with the
// same id execute the work once.
func TestExecuteNeverDoubleRunsConcurrent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	var calls int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.Execute(ctx, "op-1", "create", "vm-1", "hash-1", func(ctx context.Context) error {
				atomic.AddInt32(&calls, 1)
				return nil
			})
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("work executed %d times concurrently, want 1", got)
	}
}

// TestExecuteFailedReturnsRecordedError verifies a failed operation replays its
// recorded error.
func TestExecuteFailedReturnsRecordedError(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	want := errors.New("boom")
	if _, err := svc.Execute(ctx, "op-1", "create", "vm-1", "hash-1", func(ctx context.Context) error {
		return want
	}); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	res, err := svc.Execute(ctx, "op-1", "create", "vm-1", "hash-1", func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Succeeded() {
		t.Fatal("expected replay of failed operation to stay failed")
	}
	if res.Error == nil || res.Error.Error() != "boom" {
		t.Fatalf("expected recorded error 'boom', got %v", res.Error)
	}
}

// TestExecuteWithoutIDRunsDirectly verifies legacy callers without an
// operation id are executed directly.
func TestExecuteWithoutIDRunsDirectly(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	var calls int32
	if _, err := svc.Execute(ctx, "", "create", "vm-1", "", func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected direct execution, calls=%d", got)
	}
}
