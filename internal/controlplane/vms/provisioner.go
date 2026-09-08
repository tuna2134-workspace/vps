package vms

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
)

// AgentClient is the subset of agent gRPC calls the provisioner needs. It is
// an interface so tests can substitute a fake agent.
type AgentClient interface {
	CreateVM(ctx context.Context, req *agentv1.CreateVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error)
	DeleteVM(ctx context.Context, req *agentv1.DeleteVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error)
	StartVM(ctx context.Context, req *agentv1.StartVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error)
	StopVM(ctx context.Context, req *agentv1.StopVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error)
	ForceStopVM(ctx context.Context, req *agentv1.ForceStopVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error)
	RebootVM(ctx context.Context, req *agentv1.RebootVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error)
	GetVM(ctx context.Context, req *agentv1.GetVMRequest, timeout time.Duration) (*agentv1.VMResponse, error)
}

// AgentFactory opens a client for a node endpoint.
type AgentFactory func(ctx context.Context, endpoint string) (AgentClient, error)

// ProvisionJob is a unit of work for the operation worker.
type ProvisionJob struct {
	OperationID string
	VMID        string
	NodeID      string
}

// Provisioner runs VM operations asynchronously against agents. It is
// deliberately decoupled from the scheduler and HTTP layer.
type Provisioner struct {
	ops       *repositories.OperationRepository
	vms       *repositories.VMRepository
	catalog   *Catalog
	agents    AgentFactory
	timeout   time.Duration
	jobs      chan ProvisionJob
	log       *slog.Logger
	stop      chan struct{}
}

func NewProvisioner(
	ops *repositories.OperationRepository,
	vms *repositories.VMRepository,
	catalog *Catalog,
	agents AgentFactory,
	timeout time.Duration,
	log *slog.Logger,
) *Provisioner {
	p := &Provisioner{
		ops:     ops,
		vms:     vms,
		catalog: catalog,
		agents:  agents,
		timeout: timeout,
		jobs:    make(chan ProvisionJob, 256),
		log:     log,
		stop:    make(chan struct{}),
	}
	return p
}

// Start launches the worker pool.
func (p *Provisioner) Start(workers int) {
	if workers <= 0 {
		workers = 4
	}
	for i := 0; i < workers; i++ {
		go p.runWorker()
	}
	p.log.Info("provisioner started", "workers", workers)
}

// Stop drains and stops the worker pool.
func (p *Provisioner) Stop() {
	close(p.stop)
}

// Enqueue submits a job to the worker pool. It never blocks callers.
func (p *Provisioner) Enqueue(job ProvisionJob) {
	select {
	case p.jobs <- job:
	case <-p.stop:
	}
}

func (p *Provisioner) runWorker() {
	for {
		select {
		case <-p.stop:
			return
		case job := <-p.jobs:
			p.process(context.Background(), job)
		}
	}
}

func (p *Provisioner) process(ctx context.Context, job ProvisionJob) {
	op, err := p.ops.GetByID(ctx, job.OperationID)
	if err != nil {
		p.log.Error("operation lookup failed", "operation", job.OperationID, "error", err)
		return
	}

	if err := p.ops.MarkRunning(ctx, op.ID); err != nil {
		p.log.Error("mark operation running failed", "error", err)
		return
	}
	op.Status = models.OperationRunning

	switch op.OperationType {
	case models.OperationCreate:
		err = p.createVM(ctx, op)
	case models.OperationDelete:
		err = p.deleteVM(ctx, op)
	case models.OperationStart:
		err = p.startVM(ctx, op)
	case models.OperationStop:
		err = p.stopVM(ctx, op)
	case models.OperationForceStop:
		err = p.forceStopVM(ctx, op)
	case models.OperationReboot:
		err = p.rebootVM(ctx, op)
	default:
		err = errors.New("unknown operation type: " + string(op.OperationType))
	}

	if err != nil {
		p.log.Error("operation failed",
			"operation", op.ID, "vm", op.VMID, "type", op.OperationType, "error", err)
		if op.VMID != "" {
			_ = p.vms.UpdateStatus(ctx, op.VMID, models.VMStatusError)
		}
		_ = p.ops.MarkFailed(ctx, op.ID, err.Error())
		return
	}

	_ = p.ops.MarkSucceeded(ctx, op.ID)
	p.log.Info("operation succeeded", "operation", op.ID, "vm", op.VMID, "type", op.OperationType)
}

func (p *Provisioner) createVM(ctx context.Context, op *models.VMOperation) error {
	details, err := p.catalog.Load(ctx, op.VMID)
	if err != nil {
		return err
	}

	client, err := p.agents(ctx, details.Node.AgentEndpoint)
	if err != nil {
		return errors.New("agent unavailable: " + err.Error())
	}

	req, err := p.catalog.BuildCreateRequest(ctx, details)
	if err != nil {
		return err
	}
	resp, err := client.CreateVM(ctx, req, p.timeout)
	if err != nil {
		return err
	}
	if resp.GetError() != nil {
		return errors.New(resp.GetError().GetMessage())
	}
	return p.vms.UpdateStatus(ctx, op.VMID, models.VMStatusRunning)
}

func (p *Provisioner) deleteVM(ctx context.Context, op *models.VMOperation) error {
	vm, err := p.vms.GetByID(ctx, op.VMID)
	if err != nil {
		return err
	}
	client, err := p.agents(ctx, p.catalog.NodeEndpoint(ctx, vm.NodeID))
	if err != nil {
		return errors.New("agent unavailable: " + err.Error())
	}
	resp, err := client.DeleteVM(ctx, &agentv1.DeleteVMRequest{
		VmId:       vm.ID,
		VmName:     domainName(vm),
		DeleteDisk: true,
	}, p.timeout)
	if err != nil {
		return err
	}
	if resp.GetError() != nil {
		return errors.New(resp.GetError().GetMessage())
	}
	if err := p.catalog.Release(ctx, vm.ID); err != nil {
		return err
	}
	return p.vms.SoftDelete(ctx, vm.ID)
}

func (p *Provisioner) startVM(ctx context.Context, op *models.VMOperation) error {
	vm, err := p.vms.GetByID(ctx, op.VMID)
	if err != nil {
		return err
	}
	client, err := p.agents(ctx, p.catalog.NodeEndpoint(ctx, vm.NodeID))
	if err != nil {
		return errors.New("agent unavailable: " + err.Error())
	}
	resp, err := client.StartVM(ctx, &agentv1.StartVMRequest{VmId: vm.ID, VmName: domainName(vm)}, p.timeout)
	if err != nil {
		return err
	}
	if resp.GetError() != nil {
		return errors.New(resp.GetError().GetMessage())
	}
	return p.vms.UpdateStatus(ctx, vm.ID, models.VMStatusRunning)
}

func (p *Provisioner) stopVM(ctx context.Context, op *models.VMOperation) error {
	vm, err := p.vms.GetByID(ctx, op.VMID)
	if err != nil {
		return err
	}
	client, err := p.agents(ctx, p.catalog.NodeEndpoint(ctx, vm.NodeID))
	if err != nil {
		return errors.New("agent unavailable: " + err.Error())
	}
	resp, err := client.StopVM(ctx, &agentv1.StopVMRequest{VmId: vm.ID, VmName: domainName(vm), TimeoutSeconds: 60}, p.timeout)
	if err != nil {
		return err
	}
	if resp.GetError() != nil {
		return errors.New(resp.GetError().GetMessage())
	}
	return p.vms.UpdateStatus(ctx, vm.ID, models.VMStatusStopped)
}

func (p *Provisioner) forceStopVM(ctx context.Context, op *models.VMOperation) error {
	vm, err := p.vms.GetByID(ctx, op.VMID)
	if err != nil {
		return err
	}
	client, err := p.agents(ctx, p.catalog.NodeEndpoint(ctx, vm.NodeID))
	if err != nil {
		return errors.New("agent unavailable: " + err.Error())
	}
	resp, err := client.ForceStopVM(ctx, &agentv1.ForceStopVMRequest{VmId: vm.ID, VmName: domainName(vm)}, p.timeout)
	if err != nil {
		return err
	}
	if resp.GetError() != nil {
		return errors.New(resp.GetError().GetMessage())
	}
	return p.vms.UpdateStatus(ctx, vm.ID, models.VMStatusStopped)
}

func (p *Provisioner) rebootVM(ctx context.Context, op *models.VMOperation) error {
	vm, err := p.vms.GetByID(ctx, op.VMID)
	if err != nil {
		return err
	}
	client, err := p.agents(ctx, p.catalog.NodeEndpoint(ctx, vm.NodeID))
	if err != nil {
		return errors.New("agent unavailable: " + err.Error())
	}
	resp, err := client.RebootVM(ctx, &agentv1.RebootVMRequest{VmId: vm.ID, VmName: domainName(vm)}, p.timeout)
	if err != nil {
		return err
	}
	if resp.GetError() != nil {
		return errors.New(resp.GetError().GetMessage())
	}
	return p.vms.UpdateStatus(ctx, vm.ID, models.VMStatusRunning)
}

// domainName returns a stable, unique libvirt domain name.
func domainName(vm *models.VM) string {
	return "vps-" + vm.InstanceID
}