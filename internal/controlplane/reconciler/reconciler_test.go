package reconciler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/models"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
	commonv1 "github.com/tuna2134/vps/proto/gen/common/v1"
)

type fakeNodes struct {
	nodes []models.Node
}

func (f *fakeNodes) ListHealthy(ctx context.Context) ([]models.Node, error) { return f.nodes, nil }

type fakeVMs struct {
	vms   map[string]*models.VM
	order []string
}

func (f *fakeVMs) ListByNode(ctx context.Context, nodeID string) ([]models.VM, error) {
	var out []models.VM
	for _, id := range f.order {
		if f.vms[id].NodeID == nodeID {
			out = append(out, *f.vms[id])
		}
	}
	return out, nil
}
func (f *fakeVMs) UpdateStatus(ctx context.Context, id string, status models.VMStatus) error {
	if vm, ok := f.vms[id]; ok {
		vm.Status = status
	}
	return nil
}

type fakeAgent struct {
	resp *agentv1.ListVMsResponse
	err  error
}

func (f *fakeAgent) ListVMs(ctx context.Context, req *agentv1.ListVMsRequest, timeout time.Duration) (*agentv1.ListVMsResponse, error) {
	return f.resp, f.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestReconciler(nodes []models.Node, vms map[string]*models.VM, order []string, ag AgentClient) *Reconciler {
	return &Reconciler{
		nodes: &fakeNodes{nodes: nodes},
		vms:   &fakeVMs{vms: vms, order: order},
		agents: func(ctx context.Context, endpoint string) (AgentClient, error) {
			return ag, nil
		},
		log: testLogger(),
	}
}

// TestReconcileRunningToStopped verifies DB running + libvirt shutoff becomes
// DB stopped.
func TestReconcileRunningToStopped(t *testing.T) {
	ctx := context.Background()
	vms := map[string]*models.VM{
		"vm-1": {ID: "vm-1", NodeID: "n-1", InstanceID: "inst-1", Status: models.VMStatusRunning},
	}
	ag := &fakeAgent{resp: &agentv1.ListVMsResponse{Vms: []*agentv1.VMSummary{
		{VmName: "vps-inst-1", State: commonv1.VMState_VM_STATE_SHUTOFF},
	}}}
	r := newTestReconciler([]models.Node{{ID: "n-1", AgentEndpoint: "a:9001", Status: models.NodeStatusHealthy}}, vms, []string{"vm-1"}, ag)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if vms["vm-1"].Status != models.VMStatusStopped {
		t.Fatalf("expected stopped, got %s", vms["vm-1"].Status)
	}
}

// TestReconcileAgentUnreachableDoesNotError verifies a down agent leaves VMs
// unchanged (no immediate error).
func TestReconcileAgentUnreachableDoesNotError(t *testing.T) {
	ctx := context.Background()
	vms := map[string]*models.VM{
		"vm-1": {ID: "vm-1", NodeID: "n-1", InstanceID: "inst-1", Status: models.VMStatusRunning},
	}
	r := newTestReconciler([]models.Node{{ID: "n-1", AgentEndpoint: "a:9001", Status: models.NodeStatusHealthy}}, vms, []string{"vm-1"},
		&fakeAgent{err: errors.New("unavailable")})
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if vms["vm-1"].Status != models.VMStatusRunning {
		t.Fatalf("agent unreachable must not change status, got %s", vms["vm-1"].Status)
	}
}

// TestReconcileSkipsProvisioning verifies provisioning VMs are untouched.
func TestReconcileSkipsProvisioning(t *testing.T) {
	ctx := context.Background()
	vms := map[string]*models.VM{
		"vm-1": {ID: "vm-1", NodeID: "n-1", InstanceID: "inst-1", Status: models.VMStatusProvisioning},
	}
	ag := &fakeAgent{resp: &agentv1.ListVMsResponse{Vms: []*agentv1.VMSummary{
		{VmName: "vps-inst-1", State: commonv1.VMState_VM_STATE_RUNNING},
	}}}
	r := newTestReconciler([]models.Node{{ID: "n-1", AgentEndpoint: "a:9001", Status: models.NodeStatusHealthy}}, vms, []string{"vm-1"}, ag)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if vms["vm-1"].Status != models.VMStatusProvisioning {
		t.Fatalf("provisioning VM must be untouched, got %s", vms["vm-1"].Status)
	}
}

// TestReconcileDoesNotResurrect verifies a stopped DB VM is not set running by
// reconciliation.
func TestReconcileDoesNotResurrect(t *testing.T) {
	ctx := context.Background()
	vms := map[string]*models.VM{
		"vm-1": {ID: "vm-1", NodeID: "n-1", InstanceID: "inst-1", Status: models.VMStatusStopped},
	}
	ag := &fakeAgent{resp: &agentv1.ListVMsResponse{Vms: []*agentv1.VMSummary{
		{VmName: "vps-inst-1", State: commonv1.VMState_VM_STATE_RUNNING},
	}}}
	r := newTestReconciler([]models.Node{{ID: "n-1", AgentEndpoint: "a:9001", Status: models.NodeStatusHealthy}}, vms, []string{"vm-1"}, ag)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if vms["vm-1"].Status != models.VMStatusStopped {
		t.Fatalf("stopped VM must stay stopped, got %s", vms["vm-1"].Status)
	}
}
