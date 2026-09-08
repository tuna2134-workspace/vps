// Package storage abstracts libvirt storage operations behind a small
// interface so the VM manager does not depend on libvirt directly.
package storage

import (
	"fmt"

	"github.com/tuna2134/vps/internal/agent/libvirt"
)

// Volume is a storage volume reference.
type Volume struct {
	Pool        string
	Name        string
	Path        string
	CapacityBytes uint64
	AllocationBytes uint64
}

// Storage manages storage volumes in a libvirt storage pool.
type Storage interface {
	// CreateVolume reserves a new volume of the given capacity.
	CreateVolume(pool, name string, capacityBytes uint64) (*Volume, error)
	// VolumeExists reports whether a volume already exists.
	VolumeExists(pool, name string) (bool, error)
	// UploadVolumeFile streams a local file into a volume.
	UploadVolumeFile(pool, name, path string) error
	// DeleteVolume removes a volume (idempotent).
	DeleteVolume(pool, name string) error
	// GetVolume returns volume metadata.
	GetVolume(pool, name string) (*Volume, error)
	// VolumePath returns the on-disk path of a volume.
	VolumePath(pool, name string) (string, error)
	// RefreshPool refreshes the pool metadata.
	RefreshPool(pool string) error
}

// LibvirtStorage is the real implementation backed by the libvirt adapter.
type LibvirtStorage struct {
	manager libvirt.Manager
}

// New creates a storage backend over a libvirt manager.
func New(manager libvirt.Manager) *LibvirtStorage {
	return &LibvirtStorage{manager: manager}
}

func (s *LibvirtStorage) CreateVolume(pool, name string, capacityBytes uint64) (*Volume, error) {
	vi, err := s.manager.CreateVolume(pool, name, capacityBytes)
	if err != nil {
		return nil, err
	}
	path, _ := s.manager.GetVolume(pool, name)
	v := &Volume{Pool: pool, Name: name, CapacityBytes: capacityBytes}
	if vi != nil {
		v.CapacityBytes = vi.CapacityBytes
		v.AllocationBytes = vi.AllocationBytes
	}
	if path != nil {
		v.Path = path.Path
	}
	return v, nil
}

func (s *LibvirtStorage) VolumeExists(pool, name string) (bool, error) {
	return s.manager.VolumeExists(pool, name)
}

func (s *LibvirtStorage) UploadVolumeFile(pool, name, path string) error {
	return s.manager.UploadVolumeFile(pool, name, path)
}

func (s *LibvirtStorage) DeleteVolume(pool, name string) error {
	return s.manager.DeleteVolume(pool, name)
}

func (s *LibvirtStorage) GetVolume(pool, name string) (*Volume, error) {
	vi, err := s.manager.GetVolume(pool, name)
	if err != nil {
		return nil, err
	}
	return &Volume{
		Pool:          pool,
		Name:          name,
		Path:          vi.Path,
		CapacityBytes: vi.CapacityBytes,
		AllocationBytes: vi.AllocationBytes,
	}, nil
}

func (s *LibvirtStorage) VolumePath(pool, name string) (string, error) {
	vol, err := s.GetVolume(pool, name)
	if err != nil {
		return "", err
	}
	if vol.Path == "" {
		return "", fmt.Errorf("volume %s/%s has no path", pool, name)
	}
	return vol.Path, nil
}

func (s *LibvirtStorage) RefreshPool(pool string) error {
	return s.manager.EnsurePoolActive(pool)
}