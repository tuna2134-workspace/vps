package libvirt

import (
	"encoding/xml"
	"fmt"
	"sync"
)

// FakeManager is an in-memory implementation of Manager for tests and for
// running an agent without a hypervisor (AGENT_FAKE_MODE=true). It records
// calls so tests can assert the agent's behavior.
type FakeManager struct {
	mu          sync.Mutex
	domains     map[string]*fakeDomain
	volumes     map[string]*fakeVolume
	nodeInfo    *NodeInfo
	calls       []string
	autoProduce bool
}

type fakeDomain struct {
	XML     string
	Running bool
}

type fakeVolume struct {
	Name     string
	Capacity uint64
	DataFile string
}

// NewFakeManager returns a FakeManager.
func NewFakeManager() *FakeManager {
	return &FakeManager{
		domains:  make(map[string]*fakeDomain),
		volumes:  make(map[string]*fakeVolume),
		nodeInfo: &NodeInfo{CPUs: 4, MemoryBytes: 16 << 30, Hostname: "fake-node"},
	}
}

func (f *FakeManager) Connect() error                     { return nil }
func (f *FakeManager) Close() error                       { return nil }
func (f *FakeManager) EnsurePoolActive(pool string) error { return nil }

func (f *FakeManager) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

// Calls returns a copy of the recorded call names.
func (f *FakeManager) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *FakeManager) DefineDomain(xml string) error {
	f.record("DefineDomain")
	f.mu.Lock()
	defer f.mu.Unlock()
	name, err := extractDomainName(xml)
	if err != nil {
		return err
	}
	f.domains[name] = &fakeDomain{XML: xml}
	return nil
}

func (f *FakeManager) CreateDomain(name string) error {
	f.record("CreateDomain")
	return f.StartDomain(name)
}

func (f *FakeManager) StartDomain(name string) error {
	f.record("StartDomain")
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		d.Running = true
		return nil
	}
	return fmt.Errorf("domain %s not defined", name)
}

func (f *FakeManager) ShutdownDomain(name string) error {
	f.record("ShutdownDomain")
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		d.Running = false
		return nil
	}
	return fmt.Errorf("domain %s not found", name)
}

func (f *FakeManager) DestroyDomain(name string) error {
	f.record("DestroyDomain")
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		d.Running = false
		return nil
	}
	return fmt.Errorf("domain %s not found", name)
}

func (f *FakeManager) RebootDomain(name string) error {
	f.record("RebootDomain")
	return nil
}

func (f *FakeManager) UndefineDomain(name string) error {
	f.record("UndefineDomain")
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.domains, name)
	return nil
}

func (f *FakeManager) GetDomainState(name string) (VMState, error) {
	f.record("GetDomainState")
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		if d.Running {
			return StateRunning, nil
		}
		return StateShutoff, nil
	}
	return StateNoState, nil
}

func (f *FakeManager) GetDomainInfo(name string) (*VMInfo, error) {
	f.record("GetDomainInfo")
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.domains[name]
	if !ok {
		return nil, fmt.Errorf("domain %s not found", name)
	}
	state := StateShutoff
	if d.Running {
		state = StateRunning
	}
	return &VMInfo{Name: name, State: state, VCPUs: 2, MemoryBytes: 2 << 30, CPUTimeNS: 1000, MaxMemoryBytes: 2 << 30}, nil
}

func (f *FakeManager) GetDomainXML(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[name]; ok {
		return d.XML, nil
	}
	return "", fmt.Errorf("domain %s not found", name)
}

func (f *FakeManager) ListDomains() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.domains {
		out = append(out, k)
	}
	return out, nil
}

func (f *FakeManager) GetDomainInterfaces(name string) ([]InterfaceInfo, error) {
	return []InterfaceInfo{{Name: "eth0", MACAddress: "02:00:00:00:00:01"}}, nil
}

func (f *FakeManager) GetDomainDisks(name string) ([]DiskInfo, error) {
	return []DiskInfo{{Device: "disk", Source: "/fake/disk.qcow2"}}, nil
}

func (f *FakeManager) GetVNCInfo(name string) (string, int, error) {
	return "127.0.0.1", 5900, nil
}

func (f *FakeManager) CreateVolume(pool, name string, capacityBytes uint64) (*VolumeInfo, error) {
	f.record("CreateVolume")
	f.mu.Lock()
	defer f.mu.Unlock()
	key := pool + "/" + name
	if _, ok := f.volumes[key]; ok {
		return nil, fmt.Errorf("volume %s already exists", name)
	}
	f.volumes[key] = &fakeVolume{Name: name, Capacity: capacityBytes}
	return &VolumeInfo{Name: name, CapacityBytes: capacityBytes}, nil
}

func (f *FakeManager) VolumeExists(pool, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.volumes[pool+"/"+name]
	return ok, nil
}

func (f *FakeManager) UploadVolumeFile(pool, name, path string) error {
	f.record("UploadVolumeFile")
	f.mu.Lock()
	defer f.mu.Unlock()
	key := pool + "/" + name
	v, ok := f.volumes[key]
	if !ok {
		return fmt.Errorf("volume %s not found", name)
	}
	v.DataFile = path
	return nil
}

func (f *FakeManager) DeleteVolume(pool, name string) error {
	f.record("DeleteVolume")
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.volumes, pool+"/"+name)
	return nil
}

func (f *FakeManager) GetVolume(pool, name string) (*VolumeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.volumes[pool+"/"+name]
	if !ok {
		return nil, fmt.Errorf("volume %s not found", name)
	}
	return &VolumeInfo{Name: v.Name, CapacityBytes: v.Capacity, Path: "/fake/" + name}, nil
}

func (f *FakeManager) ResizeVolume(pool, name string, capacityBytes uint64) error {
	f.record("ResizeVolume")
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.volumes[pool+"/"+name]; ok {
		v.Capacity = capacityBytes
		return nil
	}
	return fmt.Errorf("volume %s not found", name)
}

func (f *FakeManager) GetNodeInfo() (*NodeInfo, error) {
	return &NodeInfo{CPUs: f.nodeInfo.CPUs, MemoryBytes: f.nodeInfo.MemoryBytes, Hostname: f.nodeInfo.Hostname}, nil
}

// VolumeDataFile returns the uploaded data file for a volume (test helper).
func (f *FakeManager) VolumeDataFile(pool, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.volumes[pool+"/"+name]
	if !ok {
		return ""
	}
	return v.DataFile
}

// HasDomain reports whether a domain exists (test helper).
func (f *FakeManager) HasDomain(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.domains[name]
	return ok
}

// HasVolume reports whether a volume exists (test helper).
func (f *FakeManager) HasVolume(pool, name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.volumes[pool+"/"+name]
	return ok
}

func extractDomainName(xmlDoc string) (string, error) {
	var d struct {
		Name string `xml:"name"`
	}
	if err := xml.Unmarshal([]byte(xmlDoc), &d); err != nil {
		return "", err
	}
	if d.Name == "" {
		return "", fmt.Errorf("no name in domain xml")
	}
	return d.Name, nil
}
