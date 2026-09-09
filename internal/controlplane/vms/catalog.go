package vms

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/networks"
	"github.com/tuna2134/vps/internal/controlplane/repositories"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
)

// ProvisionRequest is a snapshot of everything the agent needs to provision a
// VM, assembled by the Control Plane from its own state.
type ProvisionRequest struct {
	VM          *models.VM
	Node        *models.Node
	Allocations []models.IPAllocation
	Image       *models.Image
	Network     *models.Network
	SSHKeys     []string
}

// Catalog resolves VM-related references (node, image, network) and builds
// gRPC requests for the agent.
type Catalog struct {
	vms       *repositories.VMRepository
	nodes     *repositories.NodeRepository
	images    *repositories.ImageRepository
	networks  *networks.Service
	netRepo   *repositories.NetworkRepository
	allocRepo *repositories.IPPoolRepository
}

func NewCatalog(
	vms *repositories.VMRepository,
	nodes *repositories.NodeRepository,
	images *repositories.ImageRepository,
	networksSvc *networks.Service,
	netRepo *repositories.NetworkRepository,
	allocRepo *repositories.IPPoolRepository,
) *Catalog {
	return &Catalog{
		vms:       vms,
		nodes:     nodes,
		images:    images,
		networks:  networksSvc,
		netRepo:   netRepo,
		allocRepo: allocRepo,
	}
}

// Load assembles a ProvisionRequest for a VM.
func (c *Catalog) Load(ctx context.Context, vmID string) (*ProvisionRequest, error) {
	vm, err := c.vms.GetByID(ctx, vmID)
	if err != nil {
		return nil, err
	}
	node, err := c.nodes.GetByID(ctx, vm.NodeID)
	if err != nil {
		return nil, fmt.Errorf("load node: %w", err)
	}
	image, err := c.images.GetByID(ctx, vm.ImageID)
	if err != nil {
		return nil, fmt.Errorf("load image: %w", err)
	}
	net, err := c.netRepo.GetByID(ctx, vm.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("load network: %w", err)
	}
	allocs, err := c.allocRepo.GetByVM(ctx, vmID)
	if err != nil {
		return nil, fmt.Errorf("load allocations: %w", err)
	}
	return &ProvisionRequest{
		VM:          vm,
		Node:        node,
		Allocations: allocs,
		Image:       image,
		Network:     net,
	}, nil
}

// BuildCreateRequest converts a ProvisionRequest into the agent's
// CreateVMRequest. Network addressing comes from the IPAM allocations and the
// network's DNS/bridge configuration.
func (c *Catalog) BuildCreateRequest(ctx context.Context, pr *ProvisionRequest) (*agentv1.CreateVMRequest, error) {
	if len(pr.Allocations) == 0 {
		return nil, errors.New("vm has no ip allocations")
	}

	interfaces := []*agentv1.NetworkInterfaceConfig{{
		Bridge:     pr.Network.Bridge,
		MacAddress: pr.VM.MACAddress,
		Model:      "virtio",
	}}

	primary := pr.Allocations[0]
	netCfg := &agentv1.NetworkConfig{
		InterfaceIndex: 0,
		DnsServers:     []string{pr.Network.DNS1, pr.Network.DNS2},
	}
	if addr, err := netip.ParseAddr(primary.IPAddress); err == nil {
		if addr.Is4() {
			netCfg.Ipv4Addresses = []string{fmt.Sprintf("%s/%d", primary.IPAddress, primary.Prefix)}
			netCfg.Ipv4Gateway = primary.Gateway
		} else {
			netCfg.Ipv6Addresses = []string{fmt.Sprintf("%s/%d", primary.IPAddress, primary.Prefix)}
			netCfg.Ipv6Gateway = primary.Gateway
		}
	}

	cloudInit := &agentv1.CloudInitConfig{
		InstanceId:        pr.VM.InstanceID,
		Hostname:          pr.VM.Hostname,
		User:              "root",
		Password:          pr.VM.RootPassword,
		SshAuthorizedKeys: pr.VM.SSHKeys,
		Networks:          []*agentv1.NetworkConfig{netCfg},
	}

	return &agentv1.CreateVMRequest{
		VmId:          pr.VM.ID,
		VmName:        domainName(pr.VM),
		InstanceId:    pr.VM.InstanceID,
		Vcpu:          uint32(pr.VM.VCPU),
		MemoryBytes:   uint64(pr.VM.MemoryMB) * 1024 * 1024,
		DiskSizeBytes: uint64(pr.VM.DiskGB) * 1024 * 1024 * 1024,
		Image: &agentv1.ImageRef{
			ImageId:             pr.Image.ID,
			Name:                pr.Image.Name,
			SourceUrl:           pr.Image.SourceURL,
			Checksum:            pr.Image.Checksum,
			SizeBytes:           uint64(pr.Image.SizeBytes),
			Format:              pr.Image.Format,
			CloudInitCompatible: pr.Image.CloudInitCompatible,
			KernelUrl:           pr.Image.KernelURL,
			InitrdUrl:           pr.Image.InitrdURL,
			Cmdline:             pr.Image.Cmdline,
		},
		Interfaces: interfaces,
		CloudInit:  cloudInit,
	}, nil
}

// NodeEndpoint returns the agent endpoint for a node.
func (c *Catalog) NodeEndpoint(ctx context.Context, nodeID string) string {
	if nodeID == "" {
		return ""
	}
	node, err := c.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return ""
	}
	return node.AgentEndpoint
}

// Release frees the VM's IP allocations.
func (c *Catalog) Release(ctx context.Context, vmID string) error {
	return c.networks.Release(ctx, vmID)
}
