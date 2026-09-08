package libvirt

import (
	"testing"

	libvirtxml "libvirt.org/go/libvirtxml"
)

func TestGenerateDomainXML(t *testing.T) {
	xml, err := GenerateDomainXML(DomainConfig{
		Name:        "vps-test",
		UUID:        "3e7d9c5c-0e0a-4b6f-8c6d-1a2b3c4d5e6f",
		MemoryBytes: 2 * 1024 * 1024 * 1024,
		VCPU:        2,
		VNCPassword: "s3cret",
		Disks: []DomainDisk{
			{Device: "disk", Type: "file", Source: "/var/lib/libvirt/vps-root.qcow2", Driver: "qcow2", TargetDev: "vda", Writable: true},
			{Device: "cdrom", Type: "file", Source: "/var/lib/libvirt/vps-cidata.iso", Driver: "raw", TargetDev: "hda", Writable: false},
		},
		Interfaces: []DomainInterface{
			{Bridge: "br-public", MACAddress: "02:00:00:00:00:01", Model: "virtio"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}

	// The XML must be parseable by the official libvirtxml library.
	var d libvirtxml.Domain
	if err := d.Unmarshal(xml); err != nil {
		t.Fatalf("generated xml is not valid libvirt domain xml: %v\n%s", err, xml)
	}

	if d.Name != "vps-test" {
		t.Errorf("name mismatch: %s", d.Name)
	}
	if d.UUID != "3e7d9c5c-0e0a-4b6f-8c6d-1a2b3c4d5e6f" {
		t.Errorf("uuid mismatch: %s", d.UUID)
	}
	if d.VCPU == nil || d.VCPU.Value != 2 {
		t.Errorf("vcpu mismatch: %+v", d.VCPU)
	}
	if d.Memory == nil || d.Memory.Unit != "bytes" || d.Memory.Value != 2*1024*1024*1024 {
		t.Errorf("memory mismatch: %+v", d.Memory)
	}

	if d.Devices == nil || len(d.Devices.Disks) != 2 {
		t.Fatalf("expected 2 disks, got %+v", d.Devices)
	}
	rootDisk := d.Devices.Disks[0]
	if rootDisk.Device != "disk" || rootDisk.Driver == nil || rootDisk.Driver.Type != "qcow2" {
		t.Errorf("root disk config wrong: %+v", rootDisk)
	}
	if rootDisk.Source == nil || rootDisk.Source.File == nil || rootDisk.Source.File.File != "/var/lib/libvirt/vps-root.qcow2" {
		t.Errorf("root disk source wrong: %+v", rootDisk.Source)
	}
	cidata := d.Devices.Disks[1]
	if cidata.Device != "cdrom" || cidata.ReadOnly == nil {
		t.Errorf("cdrom should be read-only: %+v", cidata)
	}

	if len(d.Devices.Interfaces) != 1 {
		t.Fatalf("expected 1 interface")
	}
	ifc := d.Devices.Interfaces[0]
	if ifc.MAC == nil || ifc.MAC.Address != "02:00:00:00:00:01" {
		t.Errorf("interface mac wrong: %+v", ifc.MAC)
	}
	if ifc.Source == nil || ifc.Source.Bridge == nil || ifc.Source.Bridge.Bridge != "br-public" {
		t.Errorf("interface bridge wrong: %+v", ifc.Source)
	}
	if ifc.Model == nil || ifc.Model.Type != "virtio" {
		t.Errorf("interface model wrong: %+v", ifc.Model)
	}

	// VNC must listen on loopback, never public.
	if len(d.Devices.Graphics) != 1 || d.Devices.Graphics[0].VNC == nil {
		t.Fatalf("expected one vnc graphics device")
	}
	vnc := d.Devices.Graphics[0].VNC
	if vnc.Listen != "127.0.0.1" {
		t.Errorf("vnc must listen on loopback, got %q", vnc.Listen)
	}
	if vnc.Passwd == "" {
		t.Error("vnc password not set")
	}
}

func TestParseVNCInfo(t *testing.T) {
	// VNC endpoints are no longer parsed from XML; access goes through
	// virDomainOpenGraphicsFD. This test asserts the generated domain still
	// defines a loopback VNC device.
	xml, err := GenerateDomainXML(DomainConfig{
		Name: "vps-parse", MemoryBytes: 1 << 30, VCPU: 1,
		Disks:      []DomainDisk{{Device: "disk", Type: "file", Source: "/tmp/x.qcow2", Driver: "qcow2", TargetDev: "vda", Writable: true}},
		Interfaces: []DomainInterface{{Bridge: "br0", MACAddress: "02:00:00:00:00:02"}},
	})
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}
	var d libvirtxml.Domain
	if err := d.Unmarshal(xml); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(d.Devices.Graphics) != 1 || d.Devices.Graphics[0].VNC == nil {
		t.Fatalf("expected one vnc graphics device: %s", xml)
	}
	vnc := d.Devices.Graphics[0].VNC
	if vnc.Listen != "127.0.0.1" {
		t.Errorf("vnc must listen on loopback, got %q", vnc.Listen)
	}
}

func TestDomainHasSerialConsole(t *testing.T) {
	xml, err := GenerateDomainXML(DomainConfig{
		Name: "vps-serial-cfg", MemoryBytes: 1 << 30, VCPU: 1,
		Disks:      []DomainDisk{{Device: "disk", Type: "file", Source: "/tmp/x.qcow2", Driver: "qcow2", TargetDev: "vda", Writable: true}},
		Interfaces: []DomainInterface{{Bridge: "br0", MACAddress: "02:00:00:00:00:03"}},
	})
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}
	var d libvirtxml.Domain
	if err := d.Unmarshal(xml); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(d.Devices.Serials) == 0 || d.Devices.Serials[0].Source == nil || d.Devices.Serials[0].Source.Pty == nil {
		t.Fatalf("generated domain must include a pty serial console: %s", xml)
	}
	if len(d.Devices.Consoles) == 0 || d.Devices.Consoles[0].Source == nil || d.Devices.Consoles[0].Source.Pty == nil {
		t.Fatalf("generated domain must include a pty console: %s", xml)
	}
}

func TestGenerateDomainXMLKernelBootWithoutInterfaces(t *testing.T) {
	// Kernel/initrd boot domains are valid without any NIC (used by the real
	// libvirt test to boot a minimal serial console VM).
	xml, err := GenerateDomainXML(DomainConfig{
		Name: "vps-kernel", MemoryBytes: 1 << 30, VCPU: 1,
		Kernel: "/boot/vmlinuz", Initrd: "/tmp/initramfs.cpio.gz", Cmdline: "console=ttyS0",
	})
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}
	var d libvirtxml.Domain
	if err := d.Unmarshal(xml); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.OS == nil || d.OS.Kernel != "/boot/vmlinuz" || d.OS.Initrd != "/tmp/initramfs.cpio.gz" {
		t.Errorf("kernel boot config wrong: %+v", d.OS)
	}
	if d.OS.Cmdline != "console=ttyS0" {
		t.Errorf("cmdline wrong: %q", d.OS.Cmdline)
	}
}
