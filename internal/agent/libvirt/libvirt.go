// Package libvirt provides a thin adapter around the official Go libvirt
// bindings (libvirt.org/go/libvirt). The interface is small so tests can use
// a fake and so the Control Plane never touches libvirt directly.
package libvirt

import (
	"context"
	"errors"
	"fmt"
	"io"

	libvirt "libvirt.org/go/libvirt"
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
	GetVNCInfo(name string) (host string, port int, err error)

	// Storage.
	CreateVolume(pool, name string, capacityBytes uint64) (*VolumeInfo, error)
	VolumeExists(pool, name string) (bool, error)
	UploadVolumeFile(pool, name, path string) error
	DeleteVolume(pool, name string) error
	GetVolume(pool, name string) (*VolumeInfo, error)
	ResizeVolume(pool, name string, capacityBytes uint64) error

	// Node.
	GetNodeInfo() (*NodeInfo, error)
	// EnsurePoolActive refreshes the storage pool so new volumes are visible.
	EnsurePoolActive(pool string) error
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

// GetVNCInfo parses the domain XML for the VNC graphics endpoint.
func (a *Adapter) GetVNCInfo(name string) (string, int, error) {
	xml, err := a.GetDomainXML(name)
	if err != nil {
		return "", 0, err
	}
	return parseVNCInfo(xml)
}

func (a *Adapter) CreateVolume(pool, name string, capacityBytes uint64) (*VolumeInfo, error) {
	var info *VolumeInfo
	err := a.withConn(func(c *libvirt.Connect) error {
		poolObj, err := c.LookupStoragePoolByName(pool)
		if err != nil {
			return fmt.Errorf("lookup pool %s: %w", pool, err)
		}
		defer poolObj.Free()
		xml := fmt.Sprintf(`
<volume>
  <name>%s</name>
  <capacity unit="bytes">%d</capacity>
  <allocation unit="bytes">0</allocation>
  <target>
    <format type="qcow2"/>
  </target>
</volume>`, name, capacityBytes)
		vol, err := poolObj.StorageVolCreateXML(xml, libvirt.STORAGE_VOL_CREATE_PREALLOC_METADATA)
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

		stream, err := c.NewStream(libvirt.STREAM_NONBLOCK)
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
