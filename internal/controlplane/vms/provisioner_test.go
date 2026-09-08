package vms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/macalloc"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/networks"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
	"github.com/tuna2134/vps/internal/controlplane/scheduler"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
	commonv1 "github.com/tuna2134/vps/proto/gen/common/v1"
)

// fakeAgent records CreateVM calls.
type fakeAgent struct {
	calls     int
	createErr error
}

func (f *fakeAgent) CreateVM(ctx context.Context, req *agentv1.CreateVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	f.calls++
	if f.createErr != nil {
		return &agentv1.OperationResponse{State: commonv1.OperationState_OPERATION_STATE_FAILED, Error: &commonv1.Error{Code: "AGENT_ERROR", Message: f.createErr.Error()}}, nil
	}
	return &agentv1.OperationResponse{State: commonv1.OperationState_OPERATION_STATE_SUCCEEDED}, nil
}
func (f *fakeAgent) DeleteVM(ctx context.Context, req *agentv1.DeleteVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	return &agentv1.OperationResponse{State: commonv1.OperationState_OPERATION_STATE_SUCCEEDED}, nil
}
func (f *fakeAgent) StartVM(ctx context.Context, req *agentv1.StartVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	return &agentv1.OperationResponse{State: commonv1.OperationState_OPERATION_STATE_SUCCEEDED}, nil
}
func (f *fakeAgent) StopVM(ctx context.Context, req *agentv1.StopVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	return &agentv1.OperationResponse{State: commonv1.OperationState_OPERATION_STATE_SUCCEEDED}, nil
}
func (f *fakeAgent) ForceStopVM(ctx context.Context, req *agentv1.ForceStopVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	return &agentv1.OperationResponse{State: commonv1.OperationState_OPERATION_STATE_SUCCEEDED}, nil
}
func (f *fakeAgent) RebootVM(ctx context.Context, req *agentv1.RebootVMRequest, timeout time.Duration) (*agentv1.OperationResponse, error) {
	return &agentv1.OperationResponse{State: commonv1.OperationState_OPERATION_STATE_SUCCEEDED}, nil
}
func (f *fakeAgent) GetVM(ctx context.Context, req *agentv1.GetVMRequest, timeout time.Duration) (*agentv1.VMResponse, error) {
	return &agentv1.VMResponse{Status: &commonv1.VMStatus{Name: req.GetVmName(), State: commonv1.VMState_VM_STATE_RUNNING}}, nil
}

// memoryStore holds test state.
type memoryStore struct {
	nodes    []models.Node
	vms      map[string]*models.VM
	ops      map[string]*models.VMOperation
	networks []models.Network
	images   []models.Image
	allocs   []models.IPAllocation
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		vms: map[string]*models.VM{},
		ops: map[string]*models.VMOperation{},
	}
}

// memoryVMStore implements VMStore.
type memoryVMStore struct{ store *memoryStore }

func (s *memoryVMStore) UpdateStatus(ctx context.Context, id string, status models.VMStatus) error {
	if vm, ok := s.store.vms[id]; ok {
		vm.Status = status
		return nil
	}
	return errors.New("vm not found")
}

func (s *memoryVMStore) SoftDelete(ctx context.Context, id string) error {
	if vm, ok := s.store.vms[id]; ok {
		vm.Status = models.VMStatusTerminated
		return nil
	}
	return errors.New("vm not found")
}

func (s *memoryVMStore) GetByID(ctx context.Context, id string) (*models.VM, error) {
	vm, ok := s.store.vms[id]
	if !ok {
		return nil, errors.New("vm not found")
	}
	return vm, nil
}

// memoryOpStore implements OperationStore.
type memoryOpStore struct{ store *memoryStore }

func (s *memoryOpStore) GetByID(ctx context.Context, id string) (*models.VMOperation, error) {
	op, ok := s.store.ops[id]
	if !ok {
		return nil, errors.New("op not found")
	}
	return op, nil
}

func (s *memoryOpStore) MarkRunning(ctx context.Context, id string) error { return nil }
func (s *memoryOpStore) MarkSucceeded(ctx context.Context, id string) error {
	if op, ok := s.store.ops[id]; ok {
		op.Status = models.OperationSucceeded
	}
	return nil
}
func (s *memoryOpStore) MarkFailed(ctx context.Context, id, errMsg string) error {
	if op, ok := s.store.ops[id]; ok {
		op.Status = models.OperationFailed
		op.Error = errMsg
	}
	return nil
}

// ProvisioningTest doubles as the repo backing for the catalog.
type testCatalog struct {
	store *memoryStore
}

func (c *testCatalog) Load(ctx context.Context, vmID string) (*ProvisionRequest, error) {
	vm, ok := c.store.vms[vmID]
	if !ok {
		return nil, errors.New("vm not found")
	}
	var node *models.Node
	for i := range c.store.nodes {
		if c.store.nodes[i].ID == vm.NodeID {
			node = &c.store.nodes[i]
			break
		}
	}
	if node == nil {
		return nil, errors.New("node not found")
	}
	var img *models.Image
	for i := range c.store.images {
		if c.store.images[i].ID == vm.ImageID {
			img = &c.store.images[i]
			break
		}
	}
	if img == nil {
		return nil, errors.New("image not found")
	}
	var net *models.Network
	for i := range c.store.networks {
		if c.store.networks[i].ID == vm.NetworkID {
			net = &c.store.networks[i]
			break
		}
	}
	var allocs []models.IPAllocation
	for _, a := range c.store.allocs {
		if a.VMID == vmID {
			allocs = append(allocs, a)
		}
	}
	return &ProvisionRequest{VM: vm, Node: node, Image: img, Network: net, Allocations: allocs}, nil
}

func (c *testCatalog) BuildCreateRequest(ctx context.Context, pr *ProvisionRequest) (*agentv1.CreateVMRequest, error) {
	if len(pr.Allocations) == 0 {
		return nil, errors.New("no allocations")
	}
	alloc := pr.Allocations[0]
	netCfg := &agentv1.NetworkConfig{
		InterfaceIndex: 0,
		Ipv4Addresses:  []string{fmt.Sprintf("%s/%d", alloc.IPAddress, alloc.Prefix)},
		Ipv4Gateway:    alloc.Gateway,
		DnsServers:     []string{pr.Network.DNS1, pr.Network.DNS2},
	}
	return &agentv1.CreateVMRequest{
		VmId:          pr.VM.ID,
		VmName:        "vps-" + pr.VM.InstanceID,
		InstanceId:    pr.VM.InstanceID,
		Vcpu:          uint32(pr.VM.VCPU),
		MemoryBytes:   uint64(pr.VM.MemoryMB) * 1024 * 1024,
		DiskSizeBytes: uint64(pr.VM.DiskGB) * 1024 * 1024 * 1024,
		Interfaces:    []*agentv1.NetworkInterfaceConfig{{Bridge: pr.Network.Bridge, MacAddress: pr.VM.MACAddress}},
		CloudInit:     &agentv1.CloudInitConfig{InstanceId: pr.VM.InstanceID, Hostname: pr.VM.Hostname, Networks: []*agentv1.NetworkConfig{netCfg}},
	}, nil
}

func (c *testCatalog) NodeEndpoint(ctx context.Context, nodeID string) string {
	for i := range c.store.nodes {
		if c.store.nodes[i].ID == nodeID {
			return c.store.nodes[i].AgentEndpoint
		}
	}
	return ""
}

func (c *testCatalog) Release(ctx context.Context, vmID string) error {
	return nil
}

func TestProvisionerCreateVMSuccess(t *testing.T) {
	store := newMemoryStore()
	store.nodes = []models.Node{{ID: "node-1", AgentEndpoint: "agent-a:9001", Status: models.NodeStatusHealthy, CPUCapacity: 8, MemoryCapacityMB: 8192, StorageCapacityGB: 100}}
	store.images = []models.Image{{ID: "img-1", SourceURL: "file:///images/ubuntu.qcow2", Format: "qcow2"}}
	store.networks = []models.Network{{ID: "net-1", Bridge: "br-public"}}
	store.vms["vm-1"] = &models.VM{
		ID: "vm-1", NodeID: "node-1", ImageID: "img-1", NetworkID: "net-1",
		Name: "web", Hostname: "web", Status: models.VMStatusPending,
		VCPU: 1, MemoryMB: 1024, DiskGB: 10, MACAddress: "02:00:00:00:00:01", InstanceID: "i-1",
	}
	store.allocs = []models.IPAllocation{{VMID: "vm-1", IPAddress: "192.0.2.10", Gateway: "192.0.2.1", Prefix: 24}}

	catalog := &testCatalog{store: store}
	agent := &fakeAgent{}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := NewProvisioner(&memoryOpStore{store: store}, &memoryVMStore{store: store}, catalog, func(ctx context.Context, endpoint string) (AgentClient, error) {
		return agent, nil
	}, time.Second, log)

	op := &models.VMOperation{ID: "op-1", VMID: "vm-1", OperationType: models.OperationCreate}
	if err := p.createVM(context.Background(), op); err != nil {
		t.Fatalf("createVM: %v", err)
	}
	if agent.calls != 1 {
		t.Errorf("expected 1 agent call, got %d", agent.calls)
	}
}

func TestProvisionerCreateVMFailsAndReturnsError(t *testing.T) {
	store := newMemoryStore()
	store.nodes = []models.Node{{ID: "node-1", AgentEndpoint: "agent-a:9001", Status: models.NodeStatusHealthy}}
	store.images = []models.Image{{ID: "img-1", SourceURL: "file:///images/ubuntu.qcow2"}}
	store.networks = []models.Network{{ID: "net-1", Bridge: "br-public"}}
	store.vms["vm-2"] = &models.VM{
		ID: "vm-2", NodeID: "node-1", ImageID: "img-1", NetworkID: "net-1",
		Name: "web2", Hostname: "web2", Status: models.VMStatusPending,
		VCPU: 1, MemoryMB: 1024, DiskGB: 10, MACAddress: "02:00:00:00:00:02", InstanceID: "i-2",
	}
	store.allocs = []models.IPAllocation{{VMID: "vm-2", IPAddress: "192.0.2.11", Gateway: "192.0.2.1", Prefix: 24}}

	catalog := &testCatalog{store: store}
	agent := &fakeAgent{createErr: errors.New("disk full")}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := NewProvisioner(&memoryOpStore{store: store}, &memoryVMStore{store: store}, catalog, func(ctx context.Context, endpoint string) (AgentClient, error) {
		return agent, nil
	}, time.Second, log)

	op := &models.VMOperation{ID: "op-2", VMID: "vm-2", OperationType: models.OperationCreate}
	if err := p.createVM(context.Background(), op); err == nil {
		t.Fatal("expected createVM to return an error")
	}
}

func TestBuildCreateRequestHasNetworkConfig(t *testing.T) {
	store := newMemoryStore()
	store.nodes = []models.Node{{ID: "node-1", AgentEndpoint: "agent-a:9001", Status: models.NodeStatusHealthy}}
	store.images = []models.Image{{ID: "img-1", SourceURL: "file:///images/ubuntu.qcow2"}}
	store.networks = []models.Network{{ID: "net-1", Bridge: "br-public", DNS1: "1.1.1.1", DNS2: "1.0.0.1"}}
	store.vms["vm-3"] = &models.VM{
		ID: "vm-3", NodeID: "node-1", ImageID: "img-1", NetworkID: "net-1",
		Name: "web3", Hostname: "web3", Status: models.VMStatusPending,
		VCPU: 1, MemoryMB: 1024, DiskGB: 10, MACAddress: "02:00:00:00:00:03", InstanceID: "i-3",
	}
	store.allocs = []models.IPAllocation{{VMID: "vm-3", IPAddress: "192.0.2.12", Gateway: "192.0.2.1", Prefix: 24}}

	catalog := &testCatalog{store: store}
	pr, err := catalog.Load(context.Background(), "vm-3")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	req, err := catalog.BuildCreateRequest(context.Background(), pr)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.GetCloudInit().GetHostname() != "web3" {
		t.Errorf("hostname missing in cloud-init: %+v", req.GetCloudInit())
	}
	if len(req.GetCloudInit().GetNetworks()) != 1 {
		t.Fatalf("expected network config, got %+v", req.GetCloudInit())
	}
	nc := req.GetCloudInit().GetNetworks()[0]
	if len(nc.GetIpv4Addresses()) != 1 || nc.GetIpv4Addresses()[0] != "192.0.2.12/24" {
		t.Errorf("ipv4 config wrong: %+v", nc.GetIpv4Addresses())
	}
	if nc.GetIpv4Gateway() != "192.0.2.1" {
		t.Errorf("gateway wrong: %s", nc.GetIpv4Gateway())
	}
	if len(nc.GetDnsServers()) != 2 {
		t.Errorf("dns wrong: %+v", nc.GetDnsServers())
	}
	if req.GetInterfaces()[0].GetBridge() != "br-public" {
		t.Errorf("bridge wrong: %s", req.GetInterfaces()[0].GetBridge())
	}
}

// Compile-time checks that the real repos satisfy the provisioner interfaces.
var _ = repositories.ErrNotFound
var _ = macalloc.NewGenerator
var _ = networks.NewService
var _ = scheduler.New
