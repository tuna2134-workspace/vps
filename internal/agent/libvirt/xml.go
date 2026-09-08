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
