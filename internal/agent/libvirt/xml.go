package libvirt

import (
	"encoding/xml"
	"fmt"
	"os"
)

func openFile(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file %s: %w", path, err)
	}
	return f, nil
}

type domainXML struct {
	Disks []struct {
		Device string `xml:"device,attr"`
		Source struct {
			File string `xml:"file,attr"`
		} `xml:"source"`
		Target struct {
			Dev string `xml:"dev,attr"`
		} `xml:"target"`
	} `xml:"devices>disk"`
	Graphics []struct {
		Type   string `xml:"type,attr"`
		Listen string `xml:"listen,attr"`
		Port   int    `xml:"port,attr"`
	} `xml:"devices>graphics"`
}

func parseDomainDisks(xmlDoc string) ([]DiskInfo, error) {
	var d domainXML
	if err := xml.Unmarshal([]byte(xmlDoc), &d); err != nil {
		return nil, fmt.Errorf("parse domain xml: %w", err)
	}
	var out []DiskInfo
	for _, disk := range d.Disks {
		out = append(out, DiskInfo{
			Device:        disk.Device,
			Source:        disk.Source.File,
			CapacityBytes: 0, // populated by volume lookup where possible
		})
	}
	return out, nil
}

func parseVNCInfo(xmlDoc string) (string, int, error) {
	var d domainXML
	if err := xml.Unmarshal([]byte(xmlDoc), &d); err != nil {
		return "", 0, fmt.Errorf("parse domain xml: %w", err)
	}
	for _, g := range d.Graphics {
		if g.Type == "vnc" {
			host := g.Listen
			if host == "" || host == "0.0.0.0" || host == "::" {
				host = "127.0.0.1"
			}
			return host, g.Port, nil
		}
	}
	return "", 0, fmt.Errorf("domain has no vnc graphics device")
}
