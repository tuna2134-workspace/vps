package libvirt

import (
	"fmt"
	"os"

	libvirtxml "libvirt.org/go/libvirtxml"
)

func openFile(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file %s: %w", path, err)
	}
	return f, nil
}

// parseDomainDisks extracts disk info from a domain XML document.
func parseDomainDisks(xmlDoc string) ([]DiskInfo, error) {
	var d libvirtxml.Domain
	if err := d.Unmarshal(xmlDoc); err != nil {
		return nil, fmt.Errorf("parse domain xml: %w", err)
	}
	if d.Devices == nil {
		return nil, nil
	}
	var out []DiskInfo
	for _, disk := range d.Devices.Disks {
		source := ""
		if disk.Source != nil && disk.Source.File != nil {
			source = disk.Source.File.File
		}
		dev := ""
		if disk.Target != nil {
			dev = disk.Target.Dev
		}
		out = append(out, DiskInfo{
			Device: dev,
			Source: source,
		})
	}
	return out, nil
}

// parseVNCInfo extracts the VNC graphics endpoint from a domain XML document.
func parseVNCInfo(xmlDoc string) (string, int, error) {
	var d libvirtxml.Domain
	if err := d.Unmarshal(xmlDoc); err != nil {
		return "", 0, fmt.Errorf("parse domain xml: %w", err)
	}
	if d.Devices == nil {
		return "", 0, fmt.Errorf("domain has no vnc graphics device")
	}
	for _, g := range d.Devices.Graphics {
		if g.VNC != nil {
			host := g.VNC.Listen
			if host == "" || host == "0.0.0.0" || host == "::" {
				host = "127.0.0.1"
			}
			return host, g.VNC.Port, nil
		}
	}
	return "", 0, fmt.Errorf("domain has no vnc graphics device")
}

// parseSerialInfo extracts the serial console PTY path from a domain XML
// document. The PTY path only exists after the domain is running.
func parseSerialInfo(xmlDoc string) (string, error) {
	var d libvirtxml.Domain
	if err := d.Unmarshal(xmlDoc); err != nil {
		return "", fmt.Errorf("parse domain xml: %w", err)
	}
	if d.Devices == nil {
		return "", fmt.Errorf("domain has no serial device")
	}
	for _, s := range d.Devices.Serials {
		if s.Source != nil && s.Source.Pty != nil && s.Source.Pty.Path != "" {
			return s.Source.Pty.Path, nil
		}
	}
	for _, c := range d.Devices.Consoles {
		if c.Source != nil && c.Source.Pty != nil && c.Source.Pty.Path != "" {
			return c.Source.Pty.Path, nil
		}
	}
	return "", fmt.Errorf("domain has no pty serial console")
}
