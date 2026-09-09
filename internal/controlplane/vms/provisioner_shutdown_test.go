package vms

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/models"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
)

// TestProvisionerGracefulShutdown verifies Stop waits for in-flight jobs and
// that Enqueue after shutdown does not panic.
func TestProvisionerGracefulShutdown(t *testing.T) {
	store := newMemoryStore()
	var done int32

	// Slow agent that blocks until released so we can observe in-flight work.
	agent := &blockingAgent{release: make(chan struct{}), done: &done}
	p := NewProvisioner(
		&memoryOpStore{store: store},
		&memoryVMStore{store: store},
		&testCatalog{store: store},
		func(ctx context.Context, endpoint string) (AgentClient, error) { return agent, nil },
		nil, // no reservation repo in this test
		time.Second,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	p.Start(1)

	// Enqueue a job that blocks in the agent.
	store.ops["op-1"] = &models.VMOperation{ID: "op-1", VMID: "vm-1", OperationType: models.OperationStart, Status: models.OperationPending}
	store.vms["vm-1"] = &models.VM{ID: "vm-1", NodeID: "node-1", Status: models.VMStatusStopped}
	p.Enqueue(ProvisionJob{OperationID: "op-1", VMID: "vm-1", NodeID: "node-1"})

	// Let the worker pick it up.
	time.Sleep(50 * time.Millisecond)

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- p.Stop(ctx)
	}()

	// Stop must not return while the in-flight job is blocked.
	select {
	case <-shutdownDone:
		t.Fatal("Stop returned before in-flight job completed")
	case <-time.After(150 * time.Millisecond):
	}

	// Enqueue after shutdown began must not panic.
	p.Enqueue(ProvisionJob{OperationID: "op-2", VMID: "vm-2"})

	// Release the in-flight job; Stop should now return nil.
	close(agent.release)
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after in-flight job completed")
	}

	if atomic.LoadInt32(&done) != 1 {
		t.Fatalf("in-flight job did not run, done=%d", done)
	}
}

// blockingAgent blocks StartVM until released.
type blockingAgent struct {
	release chan struct{}
	done    *int32
}

func (a *blockingAgent) CreateVM(ctx context.Context, req *agentv1.CreateVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	<-a.release
	atomic.AddInt32(a.done, 1)
	return &agentv1.OperationResponse{}, nil
}
func (a *blockingAgent) DeleteVM(ctx context.Context, req *agentv1.DeleteVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	return &agentv1.OperationResponse{}, nil
}
func (a *blockingAgent) StartVM(ctx context.Context, req *agentv1.StartVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	<-a.release
	atomic.AddInt32(a.done, 1)
	return &agentv1.OperationResponse{}, nil
}
func (a *blockingAgent) StopVM(ctx context.Context, req *agentv1.StopVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	return &agentv1.OperationResponse{}, nil
}
func (a *blockingAgent) ForceStopVM(ctx context.Context, req *agentv1.ForceStopVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	return &agentv1.OperationResponse{}, nil
}
func (a *blockingAgent) RebootVM(ctx context.Context, req *agentv1.RebootVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	return &agentv1.OperationResponse{}, nil
}
func (a *blockingAgent) GetVM(ctx context.Context, req *agentv1.GetVMRequest, timeout time.Duration) (*agentv1.VMResponse, error) {
	return &agentv1.VMResponse{}, nil
}
