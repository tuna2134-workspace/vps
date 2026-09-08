package libvirt

import (
	"encoding/xml"
	"fmt"
)

// domainXMLTemplate is the QEMU domain description used to define VMs.
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

// GenerateDomainXML renders a libvirt domain XML document from a DomainConfig.
func GenerateDomainXML(cfg DomainConfig) (string, error) {
	if cfg.Name == "" || cfg.MemoryBytes == 0 || cfg.VCPU == 0 {
		return "", fmt.Errorf("domain requires name, memory and vcpu")
	}
	if len(cfg.Interfaces) == 0 {
		return "", fmt.Errorf("domain requires at least one interface")
	}

	var doc struct {
		XMLName xml.Name `xml:"domain"`
		Type    string   `xml:"type,attr"`
		Name    string   `xml:"name"`
		UUID    string   `xml:"uuid,omitempty"`
		Memory  struct {
			Unit  string `xml:"unit,attr"`
			Value uint64 `xml:",chardata"`
		} `xml:"memory"`
		VCPU uint32 `xml:"vcpu"`
		OS   struct {
			Type struct {
				Arch    string `xml:"arch,attr"`
				Machine string `xml:"machine,attr"`
				Value   string `xml:",chardata"`
			} `xml:"type"`
			Boot struct {
				Dev string `xml:"dev,attr"`
			} `xml:"boot"`
		} `xml:"os"`
		Features struct {
			ACPI struct{} `xml:"acpi"`
			APIC struct{} `xml:"apic"`
		} `xml:"features"`
		CPU struct {
			Mode string `xml:"mode,attr"`
		} `xml:"cpu"`
		Devices struct {
			Emulator  string `xml:"emulator"`
			DiskElems []struct {
				Type   string `xml:"type,attr"`
				Device string `xml:"device,attr"`
				Driver struct {
					Name  string `xml:"name,attr"`
					Type  string `xml:"type,attr"`
					Cache string `xml:"cache,attr"`
				} `xml:"driver"`
				Source struct {
					File string `xml:"file,attr"`
				} `xml:"source"`
				Target struct {
					Dev string `xml:"dev,attr"`
					Bus string `xml:"bus,attr"`
				} `xml:"target"`
				Readonly struct{} `xml:"readonly"`
			} `xml:"disk"`
			InterfaceElems []struct {
				Type string `xml:"type,attr"`
				MAC  struct {
					Address string `xml:"address,attr"`
				} `xml:"mac"`
				Source struct {
					Bridge string `xml:"bridge,attr"`
				} `xml:"source"`
				Model struct {
					Type string `xml:"type,attr"`
				} `xml:"model"`
			} `xml:"interface"`
			SerialElems []struct {
				Type   string `xml:"type,attr"`
				Target struct {
					Port int `xml:"port,attr"`
				} `xml:"target"`
			} `xml:"serial"`
			ConsoleElems []struct {
				Type   string `xml:"type,attr"`
				Target struct {
					Type string `xml:"type,attr"`
					Port int    `xml:"port,attr"`
				} `xml:"target"`
			} `xml:"console"`
			Graphics struct {
				Type   string `xml:"type,attr"`
				Port   int    `xml:"port,attr"`
				Listen string `xml:"listen,attr"`
				Passwd string `xml:"passwd,omitempty"`
			} `xml:"graphics"`
		} `xml:"devices"`
	}

	doc.Type = "kvm"
	doc.Name = cfg.Name
	doc.UUID = cfg.UUID
	doc.Memory.Unit = "bytes"
	doc.Memory.Value = cfg.MemoryBytes
	doc.VCPU = cfg.VCPU
	doc.OS.Type.Arch = "x86_64"
	doc.OS.Type.Machine = "q35"
	doc.OS.Type.Value = "hvm"
	doc.OS.Boot.Dev = "hd"
	doc.CPU.Mode = "host-passthrough"
	doc.Devices.Emulator = "/usr/bin/qemu-system-x86_64"

	for _, d := range cfg.Disks {
		elem := struct {
			Type   string `xml:"type,attr"`
			Device string `xml:"device,attr"`
			Driver struct {
				Name  string `xml:"name,attr"`
				Type  string `xml:"type,attr"`
				Cache string `xml:"cache,attr"`
			} `xml:"driver"`
			Source struct {
				File string `xml:"file,attr"`
			} `xml:"source"`
			Target struct {
				Dev string `xml:"dev,attr"`
				Bus string `xml:"bus,attr"`
			} `xml:"target"`
			Readonly struct{} `xml:"readonly"`
		}{}
		elem.Type = d.Type
		elem.Device = d.Device
		elem.Driver.Name = "qemu"
		driverType := d.Driver
		if driverType == "" {
			driverType = "qcow2"
		}
		elem.Driver.Type = driverType
		elem.Driver.Cache = "none"
		elem.Source.File = d.Source
		elem.Target.Dev = d.TargetDev
		elem.Target.Bus = "virtio"
		if !d.Writable && d.Device == "cdrom" {
			elem.Readonly = struct{}{}
		}
		doc.Devices.DiskElems = append(doc.Devices.DiskElems, elem)
	}

	for _, nic := range cfg.Interfaces {
		elem := struct {
			Type string `xml:"type,attr"`
			MAC  struct {
				Address string `xml:"address,attr"`
			} `xml:"mac"`
			Source struct {
				Bridge string `xml:"bridge,attr"`
			} `xml:"source"`
			Model struct {
				Type string `xml:"type,attr"`
			} `xml:"model"`
		}{}
		elem.Type = "bridge"
		elem.MAC.Address = nic.MACAddress
		elem.Source.Bridge = nic.Bridge
		model := nic.Model
		if model == "" {
			model = "virtio"
		}
		elem.Model.Type = model
		doc.Devices.InterfaceElems = append(doc.Devices.InterfaceElems, elem)
	}

	// Serial + console for potential serial console access.
	doc.Devices.SerialElems = append(doc.Devices.SerialElems, struct {
		Type   string `xml:"type,attr"`
		Target struct {
			Port int `xml:"port,attr"`
		} `xml:"target"`
	}{Type: "pty", Target: struct {
		Port int `xml:"port,attr"`
	}{Port: 0}})
	doc.Devices.ConsoleElems = append(doc.Devices.ConsoleElems, struct {
		Type   string `xml:"type,attr"`
		Target struct {
			Type string `xml:"type,attr"`
			Port int    `xml:"port,attr"`
		} `xml:"target"`
	}{Type: "pty", Target: struct {
		Type string `xml:"type,attr"`
		Port int    `xml:"port,attr"`
	}{Type: "serial", Port: 0}})

	doc.Devices.Graphics.Type = "vnc"
	doc.Devices.Graphics.Listen = "127.0.0.1"
	doc.Devices.Graphics.Port = -1 // auto-allocate
	if cfg.VNCPassword != "" {
		doc.Devices.Graphics.Passwd = cfg.VNCPassword
	}

	xmlDoc, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal domain xml: %w", err)
	}
	return xml.Header + string(xmlDoc), nil
}
