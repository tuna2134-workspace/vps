// Package agents manages gRPC connections from the Control Plane to agents.
package agents

import (
	"context"
	"sync"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/grpcclient"
	"github.com/tuna2134/vps/internal/controlplane/vms"
)

// Factory creates and caches agent clients keyed by endpoint.
type Factory struct {
	mu      sync.Mutex
	clients map[string]*grpcclient.Client
	opts    grpcclient.Options
}

// NewFactory builds a Factory. The options are shared across endpoints.
func NewFactory(opts grpcclient.Options) *Factory {
	return &Factory{
		clients: make(map[string]*grpcclient.Client),
		opts:    opts,
	}
}

// Client returns a cached or new agent client for the endpoint.
func (f *Factory) Client(ctx context.Context, endpoint string) (*grpcclient.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.clients[endpoint]; ok {
		return c, nil
	}
	opts := f.opts
	opts.Endpoint = endpoint
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	c, err := grpcclient.NewClient(ctx, opts)
	if err != nil {
		return nil, err
	}
	f.clients[endpoint] = c
	return c, nil
}

// CloseAll closes every cached connection.
func (f *Factory) CloseAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.clients {
		_ = c.Close()
	}
	f.clients = make(map[string]*grpcclient.Client)
}

// AgentFactoryFunc adapts a Factory to the vms.AgentFactory signature.
func AgentFactoryFunc(f *Factory) vms.AgentFactory {
	return func(ctx context.Context, endpoint string) (vms.AgentClient, error) {
		return f.Client(ctx, endpoint)
	}
}