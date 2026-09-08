// Package manager implements the agent-side VM provisioning workflow:
// fetch image → import volume → cloud-init seed → upload → iptables binding →
// define → start.
package manager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tuna2134/vps/internal/agent/cloudinit"
	"github.com/tuna2134/vps/internal/agent/image"
	"github.com/tuna2134/vps/internal/agent/libvirt"
	"github.com/tuna2134/vps/internal/agent/network"
	"github.com/tuna2134/vps/internal/agent/storage"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
	commonv1 "github.com/tuna2134/vps/proto/gen/common/v1"
)

var (
	ErrVolumeExists = errors.New("volume already exists")
)

// Manager orchestrates VM lifecycle operations on an agent node.
type Manager struct {
	libvirt     libvirt.Manager
	storage     storage.Storage
	networks    *network.IptablesManager
	fetcher     *image.Fetcher
	workDir     string
	defaultPool string
	log         *slog.Logger
}

// New builds a Manager. iptables may be nil in fake/test mode.
func New(
	lv libvirt.Manager,
	store storage.Storage,
	ipt *network.IptablesManager,
	fetcher *image.Fetcher,
	workDir, defaultPool string,
	log *slog.Logger,
) *Manager {
	return &Manager{
		libvirt:     lv,
		storage:     store,
		networks:    ipt,
		fetcher:     fetcher,
		workDir:     workDir,
		defaultPool: defaultPool,
		log:         log,
	}
}

// DefaultPool returns the agent's configured default storage pool.
func (m *Manager) DefaultPool() string {
	return m.defaultPool
}

// CreateVM runs the full provisioning flow for a VM.
func (m *Manager) CreateVM(ctx context.Context, req *agentv1.CreateVMRequest) error {
	pool := req.GetStoragePool()
	if pool == "" {
		pool = m.defaultPool
	}
	if pool == "" {
		return errors.New("no storage pool configured")
	}
	vmName := req.GetVmName()
	if vmName == "" {
		vmName = "vps-" + req.GetVmId()
	}

	rootVol := volumeName(req.GetVmId(), "root")
	cloudVol := volumeName(req.GetVmId(), "cloudinit")

	// Cleanup any partial state from a previous failed attempt.
	_ = m.storage.DeleteVolume(pool, rootVol)
	_ = m.storage.DeleteVolume(pool, cloudVol)

	// 1. Fetch the base image.
	img, err := m.fetcher.Fetch(ctx, req.GetImage().GetSourceUrl(), req.GetImage().GetChecksum(), int64(req.GetImage().GetSizeBytes()))
	if err != nil {
		return fmt.Errorf("fetch image: %w", err)
	}

	// 2. Create + upload the root disk volume.
	if err := m.createRootDisk(ctx, pool, rootVol, img.Path, req.GetDiskSizeBytes()); err != nil {
		return err
	}
	defer m.cleanupFile(img.Path)

	// 3. Generate + upload the cloud-init seed volume.
	isoPath, err := m.generateCloudInit(ctx, req)
	if err != nil {
		return err
	}
	defer m.cleanupFile(isoPath)
	if err := m.uploadISO(ctx, pool, cloudVol, isoPath); err != nil {
		return err
	}

	// 4. Configure iptables IP/MAC binding.
	if m.networks != nil {
		if err := m.configureFirewall(req); err != nil {
			return err
		}
	}

	// 5. Build and define the domain.
	xml, err := m.buildDomainXML(req, pool, rootVol, cloudVol)
	if err != nil {
		return err
	}
	if err := m.libvirt.DefineDomain(xml); err != nil {
		return fmt.Errorf("define domain: %w", err)
	}

	// 6. Start the VM.
	if err := m.libvirt.StartDomain(vmName); err != nil {
		return fmt.Errorf("start domain: %w", err)
	}
	m.log.Info("vm provisioned", "vm", req.GetVmId(), "name", vmName, "node", nodeName(m))
	return nil
}

func (m *Manager) createRootDisk(ctx context.Context, pool, volName, imagePath string, diskSizeBytes uint64) error {
	info, err := os.Stat(imagePath)
	if err != nil {
		return fmt.Errorf("stat image: %w", err)
	}
	imgSize := uint64(info.Size())

	if _, err := m.storage.CreateVolume(pool, volName, imgSize); err != nil {
		return fmt.Errorf("create root volume: %w", err)
	}
	if err := m.storage.UploadVolumeFile(pool, volName, imagePath); err != nil {
		_ = m.storage.DeleteVolume(pool, volName)
		return fmt.Errorf("upload root volume: %w", err)
	}
	if diskSizeBytes > imgSize {
		// Grow the qcow2 to the requested disk size.
		if err := m.storage.ResizeVolume(pool, volName, diskSizeBytes); err != nil {
			_ = m.storage.DeleteVolume(pool, volName)
			return fmt.Errorf("resize root volume: %w", err)
		}
	}
	return nil
}

func (m *Manager) generateCloudInit(ctx context.Context, req *agentv1.CreateVMRequest) (string, error) {
	cfg := cloudinit.Config{
		Metadata: cloudinit.InstanceMetadata{
			InstanceID: req.GetInstanceId(),
			Hostname:   req.GetCloudInit().GetHostname(),
		},
		User:     req.GetCloudInit().GetUser(),
		Password: req.GetCloudInit().GetPassword(),
		SSHKeys:  req.GetCloudInit().GetSshAuthorizedKeys(),
	}
	for _, nc := range req.GetCloudInit().GetNetworks() {
		cfg.Networks = append(cfg.Networks, cloudinit.Network{
			InterfaceIndex: int(nc.GetInterfaceIndex()),
			IPv4Addresses:  nc.GetIpv4Addresses(),
			IPv4Gateway:    nc.GetIpv4Gateway(),
			IPv6Addresses:  nc.GetIpv6Addresses(),
			IPv6Gateway:    nc.GetIpv6Gateway(),
			DNSServers:     nc.GetDnsServers(),
		})
	}

	isoPath := filepath.Join(m.workDir, fmt.Sprintf("cloudinit-%s.iso", req.GetVmId()))
	if err := cloudinit.Generate(isoPath, cfg, m.workDir); err != nil {
		return "", fmt.Errorf("generate cloud-init iso: %w", err)
	}
	return isoPath, nil
}

func (m *Manager) uploadISO(ctx context.Context, pool, volName, isoPath string) error {
	info, err := os.Stat(isoPath)
	if err != nil {
		return fmt.Errorf("stat cloud-init iso: %w", err)
	}
	if _, err := m.storage.CreateVolume(pool, volName, uint64(info.Size())); err != nil {
		return fmt.Errorf("create cloud-init volume: %w", err)
	}
	if err := m.storage.UploadVolumeFile(pool, volName, isoPath); err != nil {
		_ = m.storage.DeleteVolume(pool, volName)
		return fmt.Errorf("upload cloud-init volume: %w", err)
	}
	return nil
}

func (m *Manager) configureFirewall(req *agentv1.CreateVMRequest) error {
	var v4, v6 []string
	for _, nc := range req.GetCloudInit().GetNetworks() {
		for _, a := range nc.GetIpv4Addresses() {
			ip := strings.SplitN(a, "/", 2)[0]
			v4 = append(v4, ip)
		}
		for _, a := range nc.GetIpv6Addresses() {
			ip := strings.SplitN(a, "/", 2)[0]
			v6 = append(v6, ip)
		}
	}
	if len(req.GetInterfaces()) == 0 {
		return errors.New("no interfaces configured")
	}
	mac := req.GetInterfaces()[0].GetMacAddress()
	return m.networks.BindVMCreates(req.GetVmId(), mac, v4, v6)
}

func (m *Manager) buildDomainXML(req *agentv1.CreateVMRequest, pool, rootVol, cloudVol string) (string, error) {
	rootPath, err := m.storage.VolumePath(pool, rootVol)
	if err != nil {
		return "", fmt.Errorf("root volume path: %w", err)
	}
	cloudPath, err := m.storage.VolumePath(pool, cloudVol)
	if err != nil {
		return "", fmt.Errorf("cloud-init volume path: %w", err)
	}

	cfg := libvirt.DomainConfig{
		Name:        req.GetVmName(),
		UUID:        req.GetVmId(),
		MemoryBytes: req.GetMemoryBytes(),
		VCPU:        uint32(req.GetVcpu()),
		VNCPassword: randomPassword(),
		Disks: []libvirt.DomainDisk{
			{
				Device:    "disk",
				Type:      "file",
				Source:    rootPath,
				Driver:    "qcow2",
				TargetDev: "vda",
				Writable:  true,
			},
			{
				Device:    "cdrom",
				Type:      "file",
				Source:    cloudPath,
				Driver:    "raw",
				TargetDev: "hda",
				Writable:  false,
			},
		},
	}
	for _, ifc := range req.GetInterfaces() {
		cfg.Interfaces = append(cfg.Interfaces, libvirt.DomainInterface{
			Bridge:     ifc.GetBridge(),
			MACAddress: ifc.GetMacAddress(),
			Model:      ifc.GetModel(),
		})
	}
	xml, err := libvirt.GenerateDomainXML(cfg)
	if err != nil {
		return "", fmt.Errorf("generate domain xml: %w", err)
	}
	return xml, nil
}

// DeleteVM removes the domain and all associated volumes and firewall rules.
func (m *Manager) DeleteVM(ctx context.Context, req *agentv1.DeleteVMRequest) error {
	pool := m.defaultPool
	vmName := req.GetVmName()
	if vmName == "" {
		vmName = "vps-" + req.GetVmId()
	}
	// Stop the domain if running.
	state, err := m.libvirt.GetDomainState(vmName)
	if err == nil && state != libvirt.StateShutoff && state != libvirt.StateNoState {
		_ = m.libvirt.DestroyDomain(vmName)
	}
	_ = m.libvirt.UndefineDomain(vmName)

	if req.GetDeleteDisk() {
		_ = m.storage.DeleteVolume(pool, volumeName(req.GetVmId(), "root"))
		_ = m.storage.DeleteVolume(pool, volumeName(req.GetVmId(), "cloudinit"))
	}
	if m.networks != nil {
		if err := m.networks.DeleteVM(req.GetVmId()); err != nil {
			m.log.Warn("failed to delete firewall rules", "vm", req.GetVmId(), "error", err)
		}
	}
	m.log.Info("vm deleted", "vm", req.GetVmId())
	return nil
}

// GetVM returns current VM status.
func (m *Manager) GetVM(ctx context.Context, req *agentv1.GetVMRequest) (*commonv1.VMStatus, error) {
	vmName := req.GetVmName()
	if vmName == "" {
		vmName = "vps-" + req.GetVmId()
	}
	info, err := m.libvirt.GetDomainInfo(vmName)
	if err != nil {
		return nil, fmt.Errorf("get domain info: %w", err)
	}
	status := &commonv1.VMStatus{
		Name:        info.Name,
		Uuid:        info.UUID,
		State:       mapVMState(info.State),
		VcpuCount:   uint32(info.VCPUs),
		MemoryBytes: info.MemoryBytes,
		CpuTimeNs:   info.CPUTimeNS,
	}
	if ifaces, err := m.libvirt.GetDomainInterfaces(vmName); err == nil {
		for _, ifc := range ifaces {
			status.Interfaces = append(status.Interfaces, &commonv1.NetworkInterfaceInfo{
				Name:          ifc.Name,
				MacAddress:    ifc.MACAddress,
				Ipv4Addresses: ifc.IPv4,
				Ipv6Addresses: ifc.IPv6,
			})
		}
	}
	return status, nil
}

func mapVMState(s libvirt.VMState) commonv1.VMState {
	switch s {
	case libvirt.StateRunning:
		return commonv1.VMState_VM_STATE_RUNNING
	case libvirt.StateShutoff:
		return commonv1.VMState_VM_STATE_SHUTOFF
	case libvirt.StatePaused:
		return commonv1.VMState_VM_STATE_PAUSED
	case libvirt.StateShutdown:
		return commonv1.VMState_VM_STATE_SHUTDOWN
	case libvirt.StateCrashed:
		return commonv1.VMState_VM_STATE_CRASHED
	case libvirt.StateBlocked:
		return commonv1.VMState_VM_STATE_BLOCKED
	case libvirt.StatePMSuspended:
		return commonv1.VMState_VM_STATE_PMSUSPENDED
	}
	return commonv1.VMState_VM_STATE_NOSTATE
}

func (m *Manager) cleanupFile(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		m.log.Warn("failed to clean up temp file", "path", path, "error", err)
	}
}

func volumeName(vmID, kind string) string {
	return fmt.Sprintf("vps-%s-%s", vmID, kind)
}

func nodeName(m *Manager) string {
	if ni, err := m.libvirt.GetNodeInfo(); err == nil {
		return ni.Hostname
	}
	return "unknown"
}
