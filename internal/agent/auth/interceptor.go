// Package auth provides application-layer authentication for the agent's gRPC
// server. It complements mTLS: even when TLS is disabled (local dev), an
// attacker who can reach the agent port cannot manage VMs or open consoles.
package auth

import (
	"context"
	"crypto/subtle"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const bearerPrefix = "Bearer "

// Interceptor returns a gRPC server option that requires the configured shared
// token on every call. When token is empty authentication is skipped so local
// development (TLS disabled) keeps working; callers should log a warning in
// that case.
// Interceptors returns gRPC server options that require the configured shared
// token on every call (unary and streaming). When token is empty
// authentication is skipped so local development (TLS disabled) keeps working;
// callers should log a warning in that case.
func Interceptors(token string) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unaryInterceptor(token)),
		grpc.ChainStreamInterceptor(streamInterceptor(token)),
	}
}

func unaryInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := check(ctx, token); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func streamInterceptor(token string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := check(ss.Context(), token); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func check(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing credentials")
	}
	vals := md.Get("authorization")
	if len(vals) == 0 || len(vals[0]) <= len(bearerPrefix) {
		return status.Error(codes.Unauthenticated, "missing credentials")
	}
	got := vals[0][len(bearerPrefix):]
	if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid credentials")
	}
	return nil
}

// LogWarning logs once whether the agent is running without authentication.
func LogWarning(log *slog.Logger, token string, tlsEnabled bool) {
	if token != "" {
		return
	}
	if tlsEnabled {
		log.Warn("agent authentication is delegated to mTLS (AGENT_AUTH_TOKEN not set)")
		return
	}
	log.Warn("AGENT IS RUNNING WITHOUT AUTHENTICATION: set AGENT_AUTH_TOKEN or enable TLS (AGENT_TLS_ENABLED=true)")
}
