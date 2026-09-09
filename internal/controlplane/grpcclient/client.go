// Package grpcclient is the Control Plane's client for talking to Agents over
// gRPC. Production deployments should enable mutual TLS so agents only accept
// commands from a trusted Control Plane.
package grpcclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
	commonv1 "github.com/tuna2134/vps/proto/gen/common/v1"
)

// Options configures the gRPC connection to an agent.
type Options struct {
	// Endpoint is the agent's address, e.g. "agent-a.internal:9001".
	Endpoint string
	Timeout  time.Duration

	// AuthToken is the shared secret presented to the agent as a Bearer
	// token on every call (defense-in-depth even when mTLS is off).
	AuthToken string

	// Retry settings for transient failures (unavailable, deadline exceeded,
	// connection reset). Agent-side operation idempotency makes retries safe:
	// a CreateVM whose response was lost is replayed and returns the same
	// result instead of executing twice.
	RetryMaxRetries     int
	RetryInitialBackoff time.Duration
	RetryMaxBackoff     time.Duration

	// mTLS settings. When Enabled is true the client presents a client
	// certificate and verifies the agent against a CA.
	TLS        bool
	CertFile   string
	KeyFile    string
	CAFile     string
	ServerName string
}

// Client wraps a gRPC AgentService connection.
type Client struct {
	conn    *grpc.ClientConn
	service agentv1.AgentServiceClient

	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

// NewClient establishes a gRPC connection to an agent.
func NewClient(ctx context.Context, opts Options) (*Client, error) {
	dialOpts := []grpc.DialOption{
		grpc.WithReturnConnectionError(),
	}

	if opts.RetryMaxRetries == 0 {
		opts.RetryMaxRetries = 5
	}
	if opts.RetryInitialBackoff <= 0 {
		opts.RetryInitialBackoff = 500 * time.Millisecond
	}
	if opts.RetryMaxBackoff <= 0 {
		opts.RetryMaxBackoff = 5 * time.Second
	}

	if opts.Timeout > 0 {
		// Give the connection time to be established lazily via the per-call timeout.
		dialCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
		_ = dialCtx
	}

	if opts.TLS {
		creds, err := loadTLS(opts)
		if err != nil {
			return nil, err
		}
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(creds))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	if opts.AuthToken != "" {
		token := opts.AuthToken
		dialOpts = append(dialOpts,
			grpc.WithUnaryInterceptor(metadataUnary(token)),
			grpc.WithStreamInterceptor(metadataStream(token)),
		)
	}

	conn, err := grpc.NewClient(opts.Endpoint, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial agent %s: %w", opts.Endpoint, err)
	}
	return &Client{
		conn:           conn,
		service:        agentv1.NewAgentServiceClient(conn),
		maxRetries:     opts.RetryMaxRetries,
		initialBackoff: opts.RetryInitialBackoff,
		maxBackoff:     opts.RetryMaxBackoff,
	}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

func loadTLS(opts Options) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}
	caPEM, err := os.ReadFile(opts.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read ca file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}
	serverName := opts.ServerName
	if serverName == "" {
		serverName = "agent.internal"
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS12,
	}), nil
}

// metadataUnary attaches the shared auth token to unary RPCs.
func metadataUnary(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = attachToken(ctx, token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// metadataStream attaches the shared auth token to streaming RPCs.
func metadataStream(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = attachToken(ctx, token)
		return streamer(ctx, desc, cc, method, opts...)
	}
}

func attachToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

// withTimeout wraps a call with a per-call timeout.
func (c *Client) withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

// call runs fn with a per-attempt timeout, retrying transient failures with
// exponential backoff + jitter. Agent-side operation idempotency (operation_id)
// makes retries safe. Non-retryable errors (invalid argument, permission
// denied, not found) are returned immediately.
func (c *Client) call(ctx context.Context, timeout time.Duration, fn func(context.Context) error) error {
	for attempt := 0; ; attempt++ {
		callCtx, cancel := c.withTimeout(ctx, timeout)
		err := fn(callCtx)
		cancel()
		if err == nil {
			return nil
		}
		if !isRetryable(err) || attempt >= c.maxRetries {
			return err
		}
		delay := c.backoff(attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// backoff returns the delay for the given failed attempt: 500ms, 1s, 2s, 4s...
// capped at maxBackoff, with up to 30% jitter.
func (c *Client) backoff(attempt int) time.Duration {
	base := c.initialBackoff * time.Duration(1<<min(attempt, 5))
	if base > c.maxBackoff {
		base = c.maxBackoff
	}
	jitter := time.Duration(rand.Int63n(int64(base / 4)))
	return base + jitter
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// isRetryable reports whether a transport error is worth retrying. Agent
// OperationResponse failures (err == nil with a FAILED state) are handled by
// the caller and never retried here.
func isRetryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	}
	msg := status.Convert(err).Message()
	switch {
	case strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "use of closed network connection"),
		strings.Contains(msg, "connection refused"):
		return true
	}
	return false
}

func (c *Client) CreateVM(ctx context.Context, req *agentv1.CreateVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	var out *agentv1.OperationResponse
	err := c.call(ctx, timeout, func(ctx context.Context) error {
		resp, err := c.service.CreateVM(ctx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (c *Client) DeleteVM(ctx context.Context, req *agentv1.DeleteVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	var out *agentv1.OperationResponse
	err := c.call(ctx, timeout, func(ctx context.Context) error {
		resp, err := c.service.DeleteVM(ctx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (c *Client) StartVM(ctx context.Context, req *agentv1.StartVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	var out *agentv1.OperationResponse
	err := c.call(ctx, timeout, func(ctx context.Context) error {
		resp, err := c.service.StartVM(ctx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (c *Client) StopVM(ctx context.Context, req *agentv1.StopVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	var out *agentv1.OperationResponse
	err := c.call(ctx, timeout, func(ctx context.Context) error {
		resp, err := c.service.StopVM(ctx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (c *Client) ForceStopVM(ctx context.Context, req *agentv1.ForceStopVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	var out *agentv1.OperationResponse
	err := c.call(ctx, timeout, func(ctx context.Context) error {
		resp, err := c.service.ForceStopVM(ctx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (c *Client) RebootVM(ctx context.Context, req *agentv1.RebootVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	var out *agentv1.OperationResponse
	err := c.call(ctx, timeout, func(ctx context.Context) error {
		resp, err := c.service.RebootVM(ctx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (c *Client) GetVM(ctx context.Context, req *agentv1.GetVMRequest, timeout time.Duration) (*agentv1.VMResponse, error) {
	var out *agentv1.VMResponse
	err := c.call(ctx, timeout, func(ctx context.Context) error {
		resp, err := c.service.GetVM(ctx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (c *Client) ListVMs(ctx context.Context, req *agentv1.ListVMsRequest, timeout time.Duration) (*agentv1.ListVMsResponse, error) {
	var out *agentv1.ListVMsResponse
	err := c.call(ctx, timeout, func(ctx context.Context) error {
		resp, err := c.service.ListVMs(ctx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (c *Client) GetNodeStatus(ctx context.Context, req *agentv1.GetNodeStatusRequest, timeout time.Duration) (*agentv1.NodeStatusResponse, error) {
	var out *agentv1.NodeStatusResponse
	err := c.call(ctx, timeout, func(ctx context.Context) error {
		resp, err := c.service.GetNodeStatus(ctx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (c *Client) Heartbeat(ctx context.Context, req *agentv1.HeartbeatRequest, timeout time.Duration) (*agentv1.HeartbeatResponse, error) {
	var out *agentv1.HeartbeatResponse
	err := c.call(ctx, timeout, func(ctx context.Context) error {
		resp, err := c.service.Heartbeat(ctx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

// Console opens a bidirectional stream to a VM's console (serial or VNC). The
// first frame carries the VM identity and console type. The returned stream is
// bound to the caller's context (the console session is long-lived, so no
// per-call timeout is applied here).
func (c *Client) Console(ctx context.Context, vmID, vmName, consoleType string) (agentv1.AgentService_ConsoleClient, error) {
	stream, err := c.service.Console(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&agentv1.ConsoleRequest{VmId: vmID, VmName: vmName, ConsoleType: consoleType}); err != nil {
		return nil, err
	}
	return stream, nil
}

// OpError converts an agent OperationResponse error into a Go error.
func OpError(resp *agentv1.OperationResponse) error {
	if resp.GetError() != nil {
		return fmt.Errorf("agent operation failed: %s: %s", resp.GetError().GetCode(), resp.GetError().GetMessage())
	}
	return nil
}

// StateSucceeded reports whether the operation succeeded.
func StateSucceeded(resp *agentv1.OperationResponse) bool {
	return resp.GetState() == commonv1.OperationState_OPERATION_STATE_SUCCEEDED
}
