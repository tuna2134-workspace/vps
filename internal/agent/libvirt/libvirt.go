// Package libvirt provides a thin adapter around the official Go libvirt
// bindings (libvirt.org/go/libvirt). The interface is small so tests can use
// a fake and so the Control Plane never touches libvirt directly.
package libvirt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	libvirt "libvirt.org/go/libvirt"
	libvirtxml "libvirt.org/go/libvirtxml"
)

// VMState is the normalized lifecycle state.
type VMState string

const (
	StateNoState     VMState = "nostate"
	StateRunning     VMState = "running"
	StateBlocked     VMState = "blocked"
	StatePaused      VMState = "paused"
	StateShutdown    VMState = "shutdown"
	StateCrashed     VMState = "crashed"
	StatePMSuspended VMState = "pmsuspended"
	StateShutoff     VMState = "shutoff"
)

// VMInfo is a resource snapshot of a domain.
type VMInfo struct {
	Name           string
	UUID           string
	State          VMState
	VCPUs          uint
	MemoryBytes    uint64
	CPUTimeNS      uint64
	MaxMemoryBytes uint64
}

// NodeInfo is a snapshot of the physical host.
type NodeInfo struct {
	CPUs        uint
	MemoryBytes uint64
	Hostname    string
}

// VolumeInfo describes a storage volume.
type VolumeInfo struct {
	Name            string
	Key             string
	Path            string
	CapacityBytes   uint64
	AllocationBytes uint64
}

// InterfaceInfo describes a domain NIC.
type InterfaceInfo struct {
	Name       string
	MACAddress string
	IPv4       []string
	IPv6       []string
}

// ConsoleStream is a bidirectional byte stream to a domain's console, opened
// via the official virDomainOpenConsole API.
type ConsoleStream interface {
	Send(p []byte) (int, error)
	Recv(p []byte) (int, error)
	// Close releases the stream and the underlying domain handle.
	Close() error
}

// DiskInfo describes a domain disk.
type DiskInfo struct {
	Device          string
	Source          string
	CapacityBytes   uint64
	AllocationBytes uint64
}

// Manager is the interface to a libvirt hypervisor.
type Manager interface {
	// Connect to the hypervisor.
	Connect() error
	// Close releases the connection.
	Close() error

	// Domain lifecycle.
	DefineDomain(xml string) error
	CreateDomain(name string) error
	StartDomain(name string) error
	ShutdownDomain(name string) error
	DestroyDomain(name string) error
	RebootDomain(name string) error
	UndefineDomain(name string) error
	GetDomainState(name string) (VMState, error)
	GetDomainInfo(name string) (*VMInfo, error)
	GetDomainXML(name string) (string, error)
	ListDomains() ([]string, error)

	// Domain resources.
	GetDomainInterfaces(name string) ([]InterfaceInfo, error)
	GetDomainDisks(name string) ([]DiskInfo, error)
	// OpenConsole opens the VM's serial console via virDomainOpenConsole.
	OpenConsole(name string) (ConsoleStream, error)
	// OpenGraphics opens the VM's VNC graphics device via
	// virDomainOpenGraphicsFD, returning a connected byte stream.
	OpenGraphics(name string) (ConsoleStream, error)

	// Storage.
	CreateVolume(pool, name string, capacityBytes uint64) (*VolumeInfo, error)
	CreateVolumeWithFormat(pool, name string, capacityBytes uint64, format string) (*VolumeInfo, error)
	VolumeExists(pool, name string) (bool, error)
	UploadVolumeFile(pool, name, path string) error
	DeleteVolume(pool, name string) error
	GetVolume(pool, name string) (*VolumeInfo, error)
	ResizeVolume(pool, name string, capacityBytes uint64) error

	// Node.
	GetNodeInfo() (*NodeInfo, error)
	// EnsurePoolActive refreshes the storage pool so new volumes are visible.
	EnsurePoolActive(pool string) error
	// GetStoragePoolInfo returns capacity/availability for a pool.
	GetStoragePoolInfo(pool string) (totalBytes, freeBytes int64, err error)
}

// Adapter is the real libvirt implementation.
type Adapter struct {
	uri  string
	conn *libvirt.Connect
}

// NewAdapter creates an adapter for the given connection URI.
func NewAdapter(uri string) *Adapter {
	return &Adapter{uri: uri}
}

func (a *Adapter) Connect() error {
	conn, err := libvirt.NewConnect(a.uri)
	if err != nil {
		return fmt.Errorf("connect to libvirt at %s: %w", a.uri, err)
	}
	a.conn = conn
	return nil
}

func (a *Adapter) Close() error {
	if a.conn == nil {
		return nil
	}
	_, err := a.conn.Close()
	a.conn = nil
	return err
}

// withConn runs fn with a connection, opening one lazily if needed.
func (a *Adapter) withConn(fn func(*libvirt.Connect) error) error {
	if a.conn == nil {
		if err := a.Connect(); err != nil {
			return err
		}
	}
	return fn(a.conn)
}

// withConnResult is like withConn but returns a value.
func (a *Adapter) withConnResult(fn func(*libvirt.Connect) (ConsoleStream, error)) (ConsoleStream, error) {
	if a.conn == nil {
		if err := a.Connect(); err != nil {
			return nil, err
		}
	}
	return fn(a.conn)
}

func (a *Adapter) DefineDomain(xml string) error {
	return a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.DomainDefineXML(xml)
		if err != nil {
			return fmt.Errorf("define domain: %w", err)
		}
		defer dom.Free()
		return nil
	})
}

func (a *Adapter) CreateDomain(name string) error {
	return a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", name, err)
		}
		defer dom.Free()
		if err := dom.Create(); err != nil {
			return fmt.Errorf("create domain %s: %w", name, err)
		}
		return nil
	})
}

func (a *Adapter) StartDomain(name string) error {
	return a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", name, err)
		}
		defer dom.Free()
		state, _, err := dom.GetState()
		if err != nil {
			return fmt.Errorf("get domain state %s: %w", name, err)
		}
		if state == libvirt.DOMAIN_RUNNING {
			return nil // idempotent
		}
		if err := dom.Create(); err != nil {
			return fmt.Errorf("start domain %s: %w", name, err)
		}
		return nil
	})
}

func (a *Adapter) ShutdownDomain(name string) error {
	return a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", name, err)
		}
		defer dom.Free()
		if err := dom.Shutdown(); err != nil {
			return fmt.Errorf("shutdown domain %s: %w", name, err)
		}
		return nil
	})
}

func (a *Adapter) DestroyDomain(name string) error {
	return a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", name, err)
		}
		defer dom.Free()
		state, _, _ := dom.GetState()
		if state == libvirt.DOMAIN_SHUTOFF {
			return nil // idempotent
		}
		if err := dom.Destroy(); err != nil {
			return fmt.Errorf("destroy domain %s: %w", name, err)
		}
		return nil
	})
}

func (a *Adapter) RebootDomain(name string) error {
	return a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", name, err)
		}
		defer dom.Free()
		if err := dom.Reboot(libvirt.DOMAIN_REBOOT_DEFAULT); err != nil {
			return fmt.Errorf("reboot domain %s: %w", name, err)
		}
		return nil
	})
}

func (a *Adapter) UndefineDomain(name string) error {
	return a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", name, err)
		}
		defer dom.Free()
		if err := dom.UndefineFlags(libvirt.DOMAIN_UNDEFINE_MANAGED_SAVE | libvirt.DOMAIN_UNDEFINE_NVRAM); err != nil {
			// Fall back to a plain undefine if flags are unsupported.
			if err2 := dom.Undefine(); err2 != nil {
				return fmt.Errorf("undefine domain %s: %w", name, err)
			}
		}
		return nil
	})
}

func (a *Adapter) GetDomainState(name string) (VMState, error) {
	var state VMState
	err := a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", name, err)
		}
		defer dom.Free()
		s, _, err := dom.GetState()
		if err != nil {
			return fmt.Errorf("get domain state %s: %w", name, err)
		}
		state = mapDomainState(s)
		return nil
	})
	return state, err
}

func mapDomainState(s libvirt.DomainState) VMState {
	switch s {
	case libvirt.DOMAIN_NOSTATE:
		return StateNoState
	case libvirt.DOMAIN_RUNNING:
		return StateRunning
	case libvirt.DOMAIN_BLOCKED:
		return StateBlocked
	case libvirt.DOMAIN_PAUSED:
		return StatePaused
	case libvirt.DOMAIN_SHUTDOWN:
		return StateShutdown
	case libvirt.DOMAIN_CRASHED:
		return StateCrashed
	case libvirt.DOMAIN_PMSUSPENDED:
		return StatePMSuspended
	case libvirt.DOMAIN_SHUTOFF:
		return StateShutoff
	}
	return StateNoState
}

func (a *Adapter) GetDomainInfo(name string) (*VMInfo, error) {
	info := &VMInfo{}
	err := a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", name, err)
		}
		defer dom.Free()
		di, err := dom.GetInfo()
		if err != nil {
			return fmt.Errorf("get domain info %s: %w", name, err)
		}
		uuid, err := dom.GetUUIDString()
		if err != nil {
			return fmt.Errorf("get domain uuid %s: %w", name, err)
		}
		info = &VMInfo{
			Name:           name,
			UUID:           uuid,
			State:          mapDomainState(di.State),
			VCPUs:          di.NrVirtCpu,
			MemoryBytes:    di.Memory * 1024,
			MaxMemoryBytes: di.MaxMem * 1024,
			CPUTimeNS:      di.CpuTime,
		}
		return nil
	})
	return info, err
}

func (a *Adapter) GetDomainXML(name string) (string, error) {
	var xml string
	err := a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", name, err)
		}
		defer dom.Free()
		x, err := dom.GetXMLDesc(0)
		if err != nil {
			return fmt.Errorf("get domain xml %s: %w", name, err)
		}
		xml = x
		return nil
	})
	return xml, err
}

func (a *Adapter) ListDomains() ([]string, error) {
	var names []string
	err := a.withConn(func(c *libvirt.Connect) error {
		doms, err := c.ListAllDomains(libvirt.CONNECT_LIST_DOMAINS_ACTIVE | libvirt.CONNECT_LIST_DOMAINS_INACTIVE)
		if err != nil {
			return fmt.Errorf("list domains: %w", err)
		}
		for i := range doms {
			nm, err := doms[i].GetName()
			if err == nil {
				names = append(names, nm)
			}
			_ = doms[i].Free()
		}
		return nil
	})
	return names, err
}

// GetDomainInterfaces returns NIC info using the ARP/lease sources.
func (a *Adapter) GetDomainInterfaces(name string) ([]InterfaceInfo, error) {
	var out []InterfaceInfo
	err := a.withConn(func(c *libvirt.Connect) error {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", name, err)
		}
		defer dom.Free()
		ifaces, err := dom.ListAllInterfaceAddresses(libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_ARP)
		if err != nil {
			return fmt.Errorf("list interface addresses %s: %w", name, err)
		}
		for _, ifc := range ifaces {
			ii := InterfaceInfo{Name: ifc.Name, MACAddress: ifc.Hwaddr}
			for _, a := range ifc.Addrs {
				switch a.Type {
				case libvirt.IP_ADDR_TYPE_IPV4:
					ii.IPv4 = append(ii.IPv4, a.Addr)
				case libvirt.IP_ADDR_TYPE_IPV6:
					ii.IPv6 = append(ii.IPv6, a.Addr)
				}
			}
			out = append(out, ii)
		}
		return nil
	})
	return out, err
}

// GetDomainDisks parses the domain XML for disk info.
func (a *Adapter) GetDomainDisks(name string) ([]DiskInfo, error) {
	xml, err := a.GetDomainXML(name)
	if err != nil {
		return nil, err
	}
	return parseDomainDisks(xml)
}

// OpenConsole opens a stream to the VM's serial console using the official
// virDomainOpenConsole API.
func (a *Adapter) OpenConsole(name string) (ConsoleStream, error) {
	return a.withConnResult(func(c *libvirt.Connect) (ConsoleStream, error) {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return nil, fmt.Errorf("lookup domain %s: %w", name, err)
		}
		st, err := c.NewStream(0)
		if err != nil {
			_ = dom.Free()
			return nil, fmt.Errorf("open stream: %w", err)
		}
		if err := dom.OpenConsole("", st, libvirt.DOMAIN_CONSOLE_FORCE); err != nil {
			_ = st.Abort()
			_ = st.Free()
			_ = dom.Free()
			return nil, fmt.Errorf("open console %s: %w", name, err)
		}
		return &adapterConsoleStream{dom: dom, stream: st}, nil
	})
}

// OpenGraphics opens the VM's VNC graphics device using the official
// virDomainOpenGraphicsFD API, which returns a socket already connected to the
// VNC server. The VNC server is never exposed on a network port.
func (a *Adapter) OpenGraphics(name string) (ConsoleStream, error) {
	return a.withConnResult(func(c *libvirt.Connect) (ConsoleStream, error) {
		dom, err := c.LookupDomainByName(name)
		if err != nil {
			return nil, fmt.Errorf("lookup domain %s: %w", name, err)
		}
		fd, err := dom.OpenGraphicsFD(0, libvirt.DOMAIN_OPEN_GRAPHICS_SKIPAUTH)
		if err != nil {
			_ = dom.Free()
			return nil, fmt.Errorf("open graphics %s: %w", name, err)
		}
		return &adapterFileStream{dom: dom, file: fd}, nil
	})
}

// adapterConsoleStream wraps the libvirt stream + domain for a console.
type adapterConsoleStream struct {
	dom    *libvirt.Domain
	stream *libvirt.Stream
}

func (c *adapterConsoleStream) Send(p []byte) (int, error) {
	n, err := c.stream.Send(p)
	if err != nil {
		return n, fmt.Errorf("console send: %w", err)
	}
	return n, nil
}

func (c *adapterConsoleStream) Recv(p []byte) (int, error) {
	n, err := c.stream.Recv(p)
	if err != nil {
		return n, fmt.Errorf("console recv: %w", err)
	}
	return n, nil
}

func (c *adapterConsoleStream) Close() error {
	_ = c.stream.Abort()
	_ = c.stream.Free()
	return c.dom.Free()
}

// adapterFileStream wraps a libvirt-returned fd + domain for a console.
type adapterFileStream struct {
	dom  *libvirt.Domain
	file *os.File
}

func (f *adapterFileStream) Send(p []byte) (int, error) {
	n, err := f.file.Write(p)
	if err != nil {
		return n, fmt.Errorf("graphics send: %w", err)
	}
	return n, nil
}

func (f *adapterFileStream) Recv(p []byte) (int, error) {
	n, err := f.file.Read(p)
	if err != nil {
		return n, fmt.Errorf("graphics recv: %w", err)
	}
	return n, nil
}

func (f *adapterFileStream) Close() error {
	_ = f.file.Close()
	return f.dom.Free()
}

func (a *Adapter) CreateVolume(pool, name string, capacityBytes uint64) (*VolumeInfo, error) {
	return a.CreateVolumeWithFormat(pool, name, capacityBytes, "qcow2")
}

func (a *Adapter) CreateVolumeWithFormat(pool, name string, capacityBytes uint64, format string) (*VolumeInfo, error) {
	var info *VolumeInfo
	err := a.withConn(func(c *libvirt.Connect) error {
		poolObj, err := c.LookupStoragePoolByName(pool)
		if err != nil {
			return fmt.Errorf("lookup pool %s: %w", pool, err)
		}
		defer poolObj.Free()

		// Render the volume XML with the official libvirt-go-xml bindings.
		volDoc := &libvirtxml.StorageVolume{
			Name: name,
			Capacity: &libvirtxml.StorageVolumeSize{
				Unit:  "bytes",
				Value: capacityBytes,
			},
			Allocation: &libvirtxml.StorageVolumeSize{
				Unit:  "bytes",
				Value: 0,
			},
			Target: &libvirtxml.StorageVolumeTarget{
				Format: &libvirtxml.StorageVolumeTargetFormat{Type: format},
			},
		}
		xml, err := volDoc.Marshal()
		if err != nil {
			return fmt.Errorf("marshal volume xml: %w", err)
		}

		// Metadata preallocation is only valid for qcow2 (and a few others).
		flags := libvirt.StorageVolCreateFlags(0)
		if format == "qcow2" {
			flags = libvirt.STORAGE_VOL_CREATE_PREALLOC_METADATA
		}
		vol, err := poolObj.StorageVolCreateXML(xml, flags)
		if err != nil {
			return fmt.Errorf("create volume %s: %w", name, err)
		}
		defer vol.Free()
		vi, err := vol.GetInfo()
		if err != nil {
			return fmt.Errorf("get volume info %s: %w", name, err)
		}
		info = &VolumeInfo{Name: name, CapacityBytes: vi.Capacity, AllocationBytes: vi.Allocation}
		return nil
	})
	return info, err
}

func (a *Adapter) VolumeExists(pool, name string) (bool, error) {
	exists := false
	err := a.withConn(func(c *libvirt.Connect) error {
		poolObj, err := c.LookupStoragePoolByName(pool)
		if err != nil {
			return fmt.Errorf("lookup pool %s: %w", pool, err)
		}
		defer poolObj.Free()
		_, err = poolObj.LookupStorageVolByName(name)
		if err != nil {
			return nil // not found
		}
		exists = true
		return nil
	})
	return exists, err
}

// UploadVolumeFile streams a local file into a storage volume via the
// libvirt stream API.
func (a *Adapter) UploadVolumeFile(pool, name, path string) error {
	return a.withConn(func(c *libvirt.Connect) error {
		poolObj, err := c.LookupStoragePoolByName(pool)
		if err != nil {
			return fmt.Errorf("lookup pool %s: %w", pool, err)
		}
		defer poolObj.Free()

		vol, err := poolObj.LookupStorageVolByName(name)
		if err != nil {
			return fmt.Errorf("lookup volume %s: %w", name, err)
		}
		defer vol.Free()

		info, err := vol.GetInfo()
		if err != nil {
			return fmt.Errorf("get volume info %s: %w", name, err)
		}

		stream, err := c.NewStream(0)
		if err != nil {
			return fmt.Errorf("open stream: %w", err)
		}

		if err := vol.Upload(stream, 0, info.Capacity, 0); err != nil {
			_ = stream.Abort()
			_ = stream.Free()
			return fmt.Errorf("start volume upload %s: %w", name, err)
		}

		f, err := openFile(path)
		if err != nil {
			_ = stream.Abort()
			_ = stream.Free()
			return err
		}
		defer f.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = stream.SendAll(func(stream *libvirt.Stream, n int) ([]byte, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			buf := make([]byte, n)
			nRead, err := f.Read(buf)
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			if nRead == 0 {
				return nil, nil // EOF signals completion
			}
			return buf[:nRead], nil
		})
		if err != nil {
			_ = stream.Abort()
			_ = stream.Free()
			return fmt.Errorf("upload volume %s: %w", name, err)
		}
		if err := stream.Finish(); err != nil {
			_ = stream.Free()
			return fmt.Errorf("finish volume upload %s: %w", name, err)
		}
		_ = stream.Free()
		return nil
	})
}

func (a *Adapter) DeleteVolume(pool, name string) error {
	return a.withConn(func(c *libvirt.Connect) error {
		poolObj, err := c.LookupStoragePoolByName(pool)
		if err != nil {
			return fmt.Errorf("lookup pool %s: %w", pool, err)
		}
		defer poolObj.Free()
		vol, err := poolObj.LookupStorageVolByName(name)
		if err != nil {
			return nil // already gone (idempotent)
		}
		defer vol.Free()
		if err := vol.Delete(libvirt.STORAGE_VOL_DELETE_NORMAL); err != nil {
			return fmt.Errorf("delete volume %s: %w", name, err)
		}
		return nil
	})
}

func (a *Adapter) GetVolume(pool, name string) (*VolumeInfo, error) {
	var info *VolumeInfo
	err := a.withConn(func(c *libvirt.Connect) error {
		poolObj, err := c.LookupStoragePoolByName(pool)
		if err != nil {
			return fmt.Errorf("lookup pool %s: %w", pool, err)
		}
		defer poolObj.Free()
		vol, err := poolObj.LookupStorageVolByName(name)
		if err != nil {
			return fmt.Errorf("lookup volume %s: %w", name, err)
		}
		defer vol.Free()
		vi, err := vol.GetInfo()
		if err != nil {
			return fmt.Errorf("get volume info %s: %w", name, err)
		}
		path, _ := vol.GetPath()
		key, _ := vol.GetKey()
		info = &VolumeInfo{Name: name, Key: key, Path: path, CapacityBytes: vi.Capacity, AllocationBytes: vi.Allocation}
		return nil
	})
	return info, err
}

// ResizeVolume grows the volume's capacity.
func (a *Adapter) ResizeVolume(pool, name string, capacityBytes uint64) error {
	return a.withConn(func(c *libvirt.Connect) error {
		poolObj, err := c.LookupStoragePoolByName(pool)
		if err != nil {
			return fmt.Errorf("lookup pool %s: %w", pool, err)
		}
		defer poolObj.Free()
		vol, err := poolObj.LookupStorageVolByName(name)
		if err != nil {
			return fmt.Errorf("lookup volume %s: %w", name, err)
		}
		defer vol.Free()
		if err := vol.Resize(capacityBytes, libvirt.STORAGE_VOL_RESIZE_ALLOCATE); err != nil {
			return fmt.Errorf("resize volume %s: %w", name, err)
		}
		return nil
	})
}

func (a *Adapter) GetNodeInfo() (*NodeInfo, error) {
	var info *NodeInfo
	err := a.withConn(func(c *libvirt.Connect) error {
		ni, err := c.GetNodeInfo()
		if err != nil {
			return fmt.Errorf("get node info: %w", err)
		}
		host, err := c.GetHostname()
		if err != nil {
			return fmt.Errorf("get hostname: %w", err)
		}
		info = &NodeInfo{CPUs: ni.Cpus, MemoryBytes: ni.Memory * 1024, Hostname: host}
		return nil
	})
	return info, err
}

// VolumePath resolves the on-disk path of a volume (used by qemu directly).
func (a *Adapter) VolumePath(pool, name string) (string, error) {
	vol, err := a.GetVolume(pool, name)
	if err != nil {
		return "", err
	}
	if vol.Path == "" {
		return "", fmt.Errorf("volume %s has no path", name)
	}
	return vol.Path, nil
}

// GetStoragePoolInfo returns capacity and available bytes for a pool.
func (a *Adapter) GetStoragePoolInfo(pool string) (int64, int64, error) {
	var total, free int64
	err := a.withConn(func(c *libvirt.Connect) error {
		p, err := c.LookupStoragePoolByName(pool)
		if err != nil {
			return fmt.Errorf("lookup pool %s: %w", pool, err)
		}
		defer p.Free()
		info, err := p.GetInfo()
		if err != nil {
			return fmt.Errorf("get pool info %s: %w", pool, err)
		}
		total = int64(info.Capacity)
		free = int64(info.Available)
		return nil
	})
	return total, free, err
}

// EnsurePoolActive refreshes the pool so volumes created externally are seen.
func (a *Adapter) EnsurePoolActive(pool string) error {
	return a.withConn(func(c *libvirt.Connect) error {
		p, err := c.LookupStoragePoolByName(pool)
		if err != nil {
			return fmt.Errorf("lookup pool %s: %w", pool, err)
		}
		defer p.Free()
		if err := p.Refresh(0); err != nil {
			return fmt.Errorf("refresh pool %s: %w", pool, err)
		}
		return nil
	})
}
