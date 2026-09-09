// Package reconciler keeps the Control Plane's view of VMs in sync with the
// actual libvirt state on the agents. It is deliberately conservative:
// provisioning and deleting VMs are never touched, unreachable agents never
// mark VMs as errored, and orphan domains are only reported, never deleted.
package reconciler

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
	commonv1 "github.com/tuna2134/vps/proto/gen/common/v1"
)

// AgentClient is the subset of the agent API the reconciler needs.
type AgentClient interface {
	ListVMs(ctx context.Context, req *agentv1.ListVMsRequest, timeout time.Duration) (*agentv1.ListVMsResponse, error)
}

// AgentFactory resolves an agent client for a node endpoint.
type AgentFactory func(ctx context.Context, endpoint string) (AgentClient, error)

// NodeSource lists candidate nodes.
type NodeSource interface {
	ListHealthy(ctx context.Context) ([]models.Node, error)
}

// VMStore reads and updates VM records.
type VMStore interface {
	ListByNode(ctx context.Context, nodeID string) ([]models.VM, error)
	UpdateStatus(ctx context.Context, id string, status models.VMStatus) error
}

var _ NodeSource = (*repositories.NodeRepository)(nil)
var _ VMStore = (*repositories.VMRepository)(nil)

// Reconciler reconciles VM state across all nodes.
type Reconciler struct {
	nodes  NodeSource
	vms    VMStore
	agents AgentFactory
	audit  *audit.Service
	log    *slog.Logger
}

// New builds a reconciler.
func New(nodes NodeSource, vms VMStore, agents AgentFactory, auditSvc *audit.Service, log *slog.Logger) *Reconciler {
	return &Reconciler{nodes: nodes, vms: vms, agents: agents, audit: auditSvc, log: log}
}

// Reconcile runs one pass over every non-disabled node.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	nodes, err := r.nodes.ListHealthy(ctx)
	if err != nil {
		return err
	}
	for i := range nodes {
		n := &nodes[i]
		if n.Status == models.NodeStatusDisabled {
			continue
		}
		if err := r.reconcileNode(ctx, n); err != nil {
			r.log.Warn("reconcile node failed", "node", n.ID, "error", err)
		}
	}
	return nil
}

func (r *Reconciler) reconcileNode(ctx context.Context, n *models.Node) error {
	client, err := r.agents(ctx, n.AgentEndpoint)
	if err != nil {
		r.log.Warn("reconcile: agent unreachable, leaving VMs unchanged", "node", n.ID, "error", err)
		return nil // do not mark VMs errored when the agent is down
	}
	resp, err := client.ListVMs(ctx, &agentv1.ListVMsRequest{}, 10*time.Second)
	if err != nil {
		r.log.Warn("reconcile: list vms failed, leaving VMs unchanged", "node", n.ID, "error", err)
		return nil
	}

	// Map domain name (vps-<instance-id>) to actual state.
	actual := map[string]commonv1.VMState{}
	for _, vm := range resp.GetVms() {
		if inst := instanceIDFromDomain(vm.GetVmName()); inst != "" {
			actual[inst] = vm.GetState()
		}
	}

	vms, err := r.vms.ListByNode(ctx, n.ID)
	if err != nil {
		return err
	}
	for i := range vms {
		vm := &vms[i]
		if !isReconcilable(vm.Status) {
			continue
		}
		st, ok := actual[vm.InstanceID]
		if !ok {
			continue // not reported by agent (e.g. transient); leave as-is
		}
		want := desiredStatus(st, vm.Status)
		if want == "" || want == vm.Status {
			continue
		}
		r.log.Info("reconcile vm state", "vm", vm.ID, "from", vm.Status, "to", want)
		if err := r.vms.UpdateStatus(ctx, vm.ID, want); err != nil {
			r.log.Warn("reconcile: update vm status failed", "vm", vm.ID, "error", err)
			continue
		}
		r.audit.Record(ctx, audit.Event{
			Action:       "reconcile.vm_status",
			ResourceType: "vm",
			ResourceID:   vm.ID,
			Metadata:     map[string]any{"from": string(vm.Status), "to": string(want)},
		})
	}

	r.reportOrphans(ctx, n, actual, vms)
	return nil
}

// isReconcilable excludes VMs whose status is controlled by an in-flight or
// terminal flow.
func isReconcilable(status models.VMStatus) bool {
	switch status {
	case models.VMStatusPending, models.VMStatusProvisioning, models.VMStatusTerminated:
		return false
	}
	return true
}

// desiredStatus maps libvirt state to a DB status. It only lowers running VMs
// to stopped and never resurrects a VM the DB believes is stopped.
func desiredStatus(st commonv1.VMState, current models.VMStatus) models.VMStatus {
	if current == models.VMStatusRunning {
		switch st {
		case commonv1.VMState_VM_STATE_SHUTOFF,
			commonv1.VMState_VM_STATE_SHUTDOWN,
			commonv1.VMState_VM_STATE_CRASHED,
			commonv1.VMState_VM_STATE_NOSTATE:
			return models.VMStatusStopped
		}
	}
	return ""
}

// reportOrphans logs domains present on the agent but unknown in the DB. They
// are never deleted automatically.
func (r *Reconciler) reportOrphans(ctx context.Context, n *models.Node, actual map[string]commonv1.VMState, vms []models.VM) {
	known := map[string]bool{}
	for i := range vms {
		known[vms[i].InstanceID] = true
	}
	for inst, st := range actual {
		if known[inst] {
			continue
		}
		r.log.Warn("orphan domain detected (not in db; not deleting)", "node", n.ID, "instance", inst, "state", st)
		r.audit.Record(ctx, audit.Event{
			Action:       "reconcile.orphan_domain",
			ResourceType: "vm",
			ResourceID:   inst,
			Metadata:     map[string]any{"node_id": n.ID, "state": st.String()},
		})
	}
}

// instanceIDFromDomain extracts the instance id from a libvirt domain name
// (vps-<instance-id>).
func instanceIDFromDomain(name string) string {
	const prefix = "vps-"
	if strings.HasPrefix(name, prefix) {
		return strings.TrimPrefix(name, prefix)
	}
	return ""
}
