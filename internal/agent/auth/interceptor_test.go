package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func invoke(t *testing.T, token, callToken string) error {
	t.Helper()
	ctx := context.Background()
	if callToken != "" {
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+callToken))
	}
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	_, err := unaryInterceptor(token)(ctx, nil, nil, handler)
	return err
}

func TestAuthInterceptorAllowsValidToken(t *testing.T) {
	if err := invoke(t, "s3cr3t", "s3cr3t"); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
}

func TestAuthInterceptorRejectsMissingToken(t *testing.T) {
	err := invoke(t, "s3cr3t", "")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestAuthInterceptorRejectsWrongToken(t *testing.T) {
	err := invoke(t, "s3cr3t", "wrong")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestAuthInterceptorSkipsWhenUnconfigured(t *testing.T) {
	if err := invoke(t, "", ""); err != nil {
		t.Fatalf("empty token must be permissive, got %v", err)
	}
}

func TestInterceptorsBuildServerOptions(t *testing.T) {
	opts := Interceptors("token")
	if len(opts) != 2 {
		t.Fatalf("expected 2 server options, got %d", len(opts))
	}
	_ = grpc.NewServer(opts...)
}
