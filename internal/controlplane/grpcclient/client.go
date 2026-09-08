// Package grpcclient is the Control Plane's client for talking to Agents over
// gRPC. Production deployments should enable mutual TLS so agents only accept
// commands from a trusted Control Plane.
package grpcclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
	commonv1 "github.com/tuna2134/vps/proto/gen/common/v1"
)

// Options configures the gRPC connection to an agent.
type Options struct {
	// Endpoint is the agent's address, e.g. "agent-a.internal:9001".
	Endpoint string
	Timeout  time.Duration

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
}

// NewClient establishes a gRPC connection to an agent.
func NewClient(ctx context.Context, opts Options) (*Client, error) {
	dialOpts := []grpc.DialOption{
		grpc.WithReturnConnectionError(),
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

	conn, err := grpc.NewClient(opts.Endpoint, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial agent %s: %w", opts.Endpoint, err)
	}
	return &Client{conn: conn, service: agentv1.NewAgentServiceClient(conn)}, nil
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

// withTimeout wraps a call with a per-call timeout.
func (c *Client) withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

func (c *Client) CreateVM(ctx context.Context, req *agentv1.CreateVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	ctx, cancel := c.withTimeout(ctx, timeout)
	defer cancel()
	return c.service.CreateVM(ctx, req)
}

func (c *Client) DeleteVM(ctx context.Context, req *agentv1.DeleteVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	ctx, cancel := c.withTimeout(ctx, timeout)
	defer cancel()
	return c.service.DeleteVM(ctx, req)
}

func (c *Client) StartVM(ctx context.Context, req *agentv1.StartVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	ctx, cancel := c.withTimeout(ctx, timeout)
	defer cancel()
	return c.service.StartVM(ctx, req)
}

func (c *Client) StopVM(ctx context.Context, req *agentv1.StopVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	ctx, cancel := c.withTimeout(ctx, timeout)
	defer cancel()
	return c.service.StopVM(ctx, req)
}

func (c *Client) ForceStopVM(ctx context.Context, req *agentv1.ForceStopVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	ctx, cancel := c.withTimeout(ctx, timeout)
	defer cancel()
	return c.service.ForceStopVM(ctx, req)
}

func (c *Client) RebootVM(ctx context.Context, req *agentv1.RebootVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	ctx, cancel := c.withTimeout(ctx, timeout)
	defer cancel()
	return c.service.RebootVM(ctx, req)
}

func (c *Client) GetVM(ctx context.Context, req *agentv1.GetVMRequest, timeout time.Duration) (*agentv1.VMResponse, error) {
	ctx, cancel := c.withTimeout(ctx, timeout)
	defer cancel()
	return c.service.GetVM(ctx, req)
}

func (c *Client) GetNodeStatus(ctx context.Context, req *agentv1.GetNodeStatusRequest, timeout time.Duration) (*agentv1.NodeStatusResponse, error) {
	ctx, cancel := c.withTimeout(ctx, timeout)
	defer cancel()
	return c.service.GetNodeStatus(ctx, req)
}

func (c *Client) Heartbeat(ctx context.Context, req *agentv1.HeartbeatRequest, timeout time.Duration) (*agentv1.HeartbeatResponse, error) {
	ctx, cancel := c.withTimeout(ctx, timeout)
	defer cancel()
	return c.service.Heartbeat(ctx, req)
}

func (c *Client) GetConsoleToken(ctx context.Context, req *agentv1.GetConsoleTokenRequest, timeout time.Duration) (*agentv1.GetConsoleTokenResponse, error) {
	ctx, cancel := c.withTimeout(ctx, timeout)
	defer cancel()
	return c.service.GetConsoleToken(ctx, req)
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