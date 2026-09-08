package libvirt

import (
	"fmt"

	libvirtxml "libvirt.org/go/libvirtxml"
)

// DomainConfig describes a VM domain to be defined via libvirt.
type DomainConfig struct {
	Name        string
	UUID        string
	MemoryBytes uint64
	VCPU        uint32
	Disks       []DomainDisk
	Interfaces  []DomainInterface
	// CloudInit volume attached as a CD-ROM.
	CloudInitVolume string
	CloudInitPool   string
	// VNC password (empty disables password).
	VNCPassword string
}

type DomainDisk struct {
	Device    string // "disk" or "cdrom"
	Type      string // "file"
	Source    string // volume path
	Driver    string // "qcow2" or "raw"
	TargetDev string // "vda"
	Writable  bool
}

type DomainInterface struct {
	Bridge     string
	MACAddress string
	Model      string
}

// GenerateDomainXML renders a libvirt domain XML document using the official
// libvirt-go-xml bindings (libvirt.org/go/libvirtxml).
func GenerateDomainXML(cfg DomainConfig) (string, error) {
	if cfg.Name == "" || cfg.MemoryBytes == 0 || cfg.VCPU == 0 {
		return "", fmt.Errorf("domain requires name, memory and vcpu")
	}
	if len(cfg.Interfaces) == 0 {
		return "", fmt.Errorf("domain requires at least one interface")
	}

	memory := uint(cfg.MemoryBytes)
	vcpu := uint(cfg.VCPU)
	domain := &libvirtxml.Domain{
		Type: "kvm",
		Name: cfg.Name,
		UUID: cfg.UUID,
		Memory: &libvirtxml.DomainMemory{
			Value: memory,
			Unit:  "bytes",
		},
		CurrentMemory: &libvirtxml.DomainCurrentMemory{
			Value: memory,
			Unit:  "bytes",
		},
		VCPU: &libvirtxml.DomainVCPU{Value: vcpu},
		OS: &libvirtxml.DomainOS{
			Type: &libvirtxml.DomainOSType{
				Arch:    "x86_64",
				Machine: "q35",
				Type:    "hvm",
			},
			BootDevices: []libvirtxml.DomainBootDevice{{Dev: "hd"}},
		},
		Features: &libvirtxml.DomainFeatureList{
			ACPI: &libvirtxml.DomainFeature{},
			APIC: &libvirtxml.DomainFeatureAPIC{},
		},
		CPU: &libvirtxml.DomainCPU{Mode: "host-passthrough"},
		Devices: &libvirtxml.DomainDeviceList{
			Emulator: "/usr/bin/qemu-system-x86_64",
		},
	}

	// Disks.
	for _, d := range cfg.Disks {
		disk := &libvirtxml.DomainDisk{
			Device: d.Device,
			Driver: &libvirtxml.DomainDiskDriver{
				Name:  "qemu",
				Type:  d.Driver,
				Cache: "none",
			},
			Source: &libvirtxml.DomainDiskSource{
				File: &libvirtxml.DomainDiskSourceFile{File: d.Source},
			},
			Target: &libvirtxml.DomainDiskTarget{
				Dev: d.TargetDev,
				Bus: "virtio",
			},
		}
		if !d.Writable {
			disk.ReadOnly = &libvirtxml.DomainDiskReadOnly{}
		}
		domain.Devices.Disks = append(domain.Devices.Disks, *disk)
	}

	// Network interfaces (bridge).
	for _, nic := range cfg.Interfaces {
		model := nic.Model
		if model == "" {
			model = "virtio"
		}
		ifc := &libvirtxml.DomainInterface{
			MAC: &libvirtxml.DomainInterfaceMAC{Address: nic.MACAddress},
			Source: &libvirtxml.DomainInterfaceSource{
				Bridge: &libvirtxml.DomainInterfaceSourceBridge{Bridge: nic.Bridge},
			},
			Model: &libvirtxml.DomainInterfaceModel{Type: model},
		}
		domain.Devices.Interfaces = append(domain.Devices.Interfaces, *ifc)
	}

	// Serial + console (pty) for potential serial console access.
	port0 := uint(0)
	domain.Devices.Serials = []libvirtxml.DomainSerial{{
		Source: &libvirtxml.DomainChardevSource{
			Pty: &libvirtxml.DomainChardevSourcePty{},
		},
		Target: &libvirtxml.DomainSerialTarget{
			Type: "isa-serial",
			Port: &port0,
		},
	}}
	domain.Devices.Consoles = []libvirtxml.DomainConsole{{
		Source: &libvirtxml.DomainChardevSource{
			Pty: &libvirtxml.DomainChardevSourcePty{},
		},
		Target: &libvirtxml.DomainConsoleTarget{
			Type: "serial",
			Port: &port0,
		},
	}}

	// VNC graphics, listening on loopback only (never public).
	domain.Devices.Graphics = []libvirtxml.DomainGraphic{{
		VNC: &libvirtxml.DomainGraphicVNC{
			Port:     -1, // auto-allocate
			AutoPort: "yes",
			Listen:   "127.0.0.1",
			Passwd:   cfg.VNCPassword,
			Keymap:   "en-us",
		},
	}}

	doc, err := domain.Marshal()
	if err != nil {
		return "", fmt.Errorf("marshal domain xml: %w", err)
	}
	return doc, nil
}
