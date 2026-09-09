package grpcclient

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newRetryClient() *Client {
	return &Client{
		maxRetries:     5,
		initialBackoff: time.Millisecond,
		maxBackoff:     10 * time.Millisecond,
	}
}

// TestCallRetriesUnavailable verifies an unavailable RPC is retried.
func TestCallRetriesUnavailable(t *testing.T) {
	c := newRetryClient()
	var attempts int32
	err := c.call(context.Background(), time.Second, func(ctx context.Context) error {
		if atomic.AddInt32(&attempts, 1) < 3 {
			return status.Error(codes.Unavailable, "upstream down")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

// TestCallDoesNotRetryInvalidArgument verifies non-retryable errors are
// returned immediately.
func TestCallDoesNotRetryInvalidArgument(t *testing.T) {
	c := newRetryClient()
	var attempts int32
	err := c.call(context.Background(), time.Second, func(ctx context.Context) error {
		atomic.AddInt32(&attempts, 1)
		return status.Error(codes.InvalidArgument, "bad request")
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry)", got)
	}
}

// TestCallDoesNotRetryNotFound verifies NotFound is not retried.
func TestCallDoesNotRetryNotFound(t *testing.T) {
	c := newRetryClient()
	var attempts int32
	err := c.call(context.Background(), time.Second, func(ctx context.Context) error {
		atomic.AddInt32(&attempts, 1)
		return status.Error(codes.NotFound, "missing")
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

// TestCallHonorsContextCancellation verifies retries stop when ctx is done.
func TestCallHonorsContextCancellation(t *testing.T) {
	c := newRetryClient()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var attempts int32
	err := c.call(ctx, time.Second, func(ctx context.Context) error {
		atomic.AddInt32(&attempts, 1)
		return status.Error(codes.Unavailable, "down")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestIsRetryable verifies the classification of gRPC errors.
func TestIsRetryable(t *testing.T) {
	if !isRetryable(status.Error(codes.Unavailable, "x")) {
		t.Error("Unavailable should be retryable")
	}
	if !isRetryable(status.Error(codes.DeadlineExceeded, "x")) {
		t.Error("DeadlineExceeded should be retryable")
	}
	if isRetryable(status.Error(codes.InvalidArgument, "x")) {
		t.Error("InvalidArgument must not be retryable")
	}
	if isRetryable(status.Error(codes.NotFound, "x")) {
		t.Error("NotFound must not be retryable")
	}
	if isRetryable(status.Error(codes.PermissionDenied, "x")) {
		t.Error("PermissionDenied must not be retryable")
	}
	if !isRetryable(status.Error(codes.Unavailable, "connection reset by peer")) {
		t.Error("connection reset should be retryable")
	}
}
