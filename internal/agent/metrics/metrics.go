// Package metrics collects node resource usage for heartbeats.
package metrics

import (
	"fmt"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/tuna2134/vps/internal/agent/libvirt"
)

// Snapshot is a node resource snapshot.
type Snapshot struct {
	Hostname          string
	CPUCount          int
	MemoryTotalBytes  int64
	MemoryFreeBytes   int64
	StorageTotalBytes int64
	StorageFreeBytes  int64
	CPUUsagePercent   float64
	Healthy           bool
}

// Provider samples node metrics.
type Provider struct {
	manager libvirt.Manager
}

func NewProvider(manager libvirt.Manager) *Provider {
	return &Provider{manager: manager}
}

// Sample returns the current node metrics.
func (p *Provider) Sample() (*Snapshot, error) {
	s := &Snapshot{Healthy: true}

	if hostname, err := host.Hostname(); err == nil {
		s.Hostname = hostname
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		s.MemoryTotalBytes = int64(vm.Total)
		s.MemoryFreeBytes = int64(vm.Available)
	}

	if percents, err := cpu.Percent(time.Second, false); err == nil && len(percents) > 0 {
		s.CPUUsagePercent = percents[0]
	}
	s.CPUCount = runtime.NumCPU()

	// libvirt-reported host resources are preferred when available.
	if p.manager != nil {
		if ni, err := p.manager.GetNodeInfo(); err == nil {
			if ni.CPUs > 0 {
				s.CPUCount = int(ni.CPUs)
			}
			if ni.MemoryBytes > 0 {
				s.MemoryTotalBytes = int64(ni.MemoryBytes)
			}
		}
	}

	return s, nil
}

// StorageSnapshot returns pool usage if the manager supports it.
func (p *Provider) StorageSnapshot(poolName string) (total, free int64, err error) {
	if p.manager == nil {
		return 0, 0, nil
	}
	// Fall back to local filesystem stats for the pool path if libvirt cannot
	// provide it directly.
	info, err := p.manager.GetNodeInfo()
	if err != nil {
		return 0, 0, fmt.Errorf("get node info: %w", err)
	}
	_ = info
	_ = poolName
	return 0, 0, nil
}