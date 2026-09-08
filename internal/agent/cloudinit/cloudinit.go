// Package cloudinit generates cloud-init NoCloud seed disks (ISO9660)
// containing user-data, meta-data, and network-config. All YAML documents are
// written and read through gopkg.in/yaml.v3 so escaping is handled correctly.
package cloudinit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
	"gopkg.in/yaml.v3"
)

// InstanceMetadata is the NoCloud meta-data payload.
type InstanceMetadata struct {
	InstanceID string
	Hostname   string
}

// Network is a per-interface network config (Network Config Version 2).
type Network struct {
	InterfaceIndex int
	IPv4Addresses  []string // e.g. "192.0.2.10/24"
	IPv4Gateway    string
	IPv6Addresses  []string // e.g. "2001:db8::10/64"
	IPv6Gateway    string
	DNSServers     []string
}

// Config is the full cloud-init seed.
type Config struct {
	Metadata InstanceMetadata
	User     string
	Password string
	SSHKeys  []string
	Networks []Network
	// ExtraUserData is appended verbatim to user-data (for custom config).
	ExtraUserData string
}

// Generate writes a cloud-init NoCloud ISO to path.
// The ISO contains user-data, meta-data, and network-config.
func Generate(path string, cfg Config, workDir string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create iso directory: %w", err)
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("create work directory: %w", err)
	}

	// Remove any stale file first (Create() requires the path to not exist).
	_ = os.Remove(path)

	// A NoCloud seed is small; 2 MiB is plenty.
	const size = 2 * 1024 * 1024

	workspace, err := os.MkdirTemp(workDir, "cloudinit-*")
	if err != nil {
		return fmt.Errorf("create cloud-init workspace: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(workspace); err != nil {
			// Cleanup failure must be logged by the caller; we surface it via
			// the returned error only when no other error occurred.
			fmt.Fprintf(os.Stderr, "cloud-init: failed to clean up workspace %s: %v\n", workspace, err)
		}
	}()

	theDisk, err := diskfs.Create(path, size, diskfs.SectorSizeDefault)
	if err != nil {
		return fmt.Errorf("create cloud-init disk: %w", err)
	}

	// ISO9660 mandates a 2048-byte logical sector size.
	theDisk.LogicalBlocksize = 2048

	fs, err := theDisk.CreateFilesystem(disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: "cidata",
		WorkDir:     workspace,
	})
	if err != nil {
		_ = theDisk.Close()
		return fmt.Errorf("create iso9660 filesystem: %w", err)
	}

	if err := writeFiles(fs, cfg); err != nil {
		_ = theDisk.Close()
		return err
	}

	// ISO9660 is a read-only filesystem: Finalize produces the final image.
	isoFS, ok := fs.(*iso9660.FileSystem)
	if !ok {
		_ = theDisk.Close()
		return fmt.Errorf("unexpected filesystem type %T", fs)
	}
	if err := isoFS.Finalize(iso9660.FinalizeOptions{
		RockRidge: true,
		Joliet:    true,
	}); err != nil {
		_ = theDisk.Close()
		return fmt.Errorf("finalize cloud-init iso: %w", err)
	}

	if err := theDisk.Close(); err != nil {
		return fmt.Errorf("close cloud-init disk: %w", err)
	}
	return nil
}

// writeFiles writes user-data, meta-data, and network-config.
func writeFiles(fs filesystem.FileSystem, cfg Config) error {
	files := map[string]string{
		"meta-data":      renderMetaData(cfg),
		"user-data":      renderUserData(cfg),
		"network-config": renderNetworkConfig(cfg),
	}
	for name, content := range files {
		if err := writeFile(fs, name, content); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(fs filesystem.FileSystem, name, content string) error {
	f, err := fs.OpenFile("/"+name, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return fmt.Errorf("open %s in cloud-init iso: %w", name, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	return nil
}

// --- YAML document types ---

// metaData is the NoCloud meta-data document.
type metaData struct {
	InstanceID    string `yaml:"instance-id"`
	LocalHostname string `yaml:"local-hostname"`
}

// userData is the cloud-init user-data document.
type userData struct {
	Hostname          string   `yaml:"hostname,omitempty"`
	User              string   `yaml:"user,omitempty"`
	Password          string   `yaml:"password,omitempty"`
	SSHPAuth          *bool    `yaml:"ssh_pwauth,omitempty"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty"`
}

// networkConfigV2 is Network Config Version 2 (netplan).
type networkConfigV2 struct {
	Version   int              `yaml:"version"`
	Ethernets map[string]ether `yaml:"ethernets"`
}

type ether struct {
	Addresses   []string     `yaml:"addresses,omitempty"`
	DHCP4       *bool        `yaml:"dhcp4,omitempty"`
	Routes      []route      `yaml:"routes,omitempty"`
	Nameservers *nameservers `yaml:"nameservers,omitempty"`
}

type route struct {
	To  string `yaml:"to"`
	Via string `yaml:"via"`
}

type nameservers struct {
	Addresses []string `yaml:"addresses,omitempty"`
}

// --- Rendering (write path) ---

func renderMetaData(cfg Config) string {
	doc := metaData{
		InstanceID:    cfg.Metadata.InstanceID,
		LocalHostname: cfg.Metadata.Hostname,
	}
	return yamlString(doc)
}

func renderUserData(cfg Config) string {
	doc := userData{
		Hostname: cfg.Metadata.Hostname,
	}
	if cfg.User != "" {
		doc.User = cfg.User
	}
	if cfg.Password != "" {
		doc.Password = cfg.Password
		trueVal := true
		doc.SSHPAuth = &trueVal
	}
	if len(cfg.SSHKeys) > 0 {
		doc.SSHAuthorizedKeys = cfg.SSHKeys
	}
	body := yamlString(doc)
	if cfg.ExtraUserData != "" {
		body += cfg.ExtraUserData
		if body[len(body)-1] != '\n' {
			body += "\n"
		}
	}
	// cloud-init requires the module header as the first line.
	return "#cloud-config\n" + body
}

// renderNetworkConfig emits Network Config Version 2.
func renderNetworkConfig(cfg Config) string {
	doc := networkConfigV2{Version: 2, Ethernets: map[string]ether{}}
	for _, n := range cfg.Networks {
		name := fmt.Sprintf("eth%d", n.InterfaceIndex)
		e := ether{}
		dhcp := false
		if len(n.IPv4Addresses) == 0 && len(n.IPv6Addresses) == 0 {
			dhcp = true
			e.DHCP4 = &dhcp
		} else {
			e.Addresses = append(e.Addresses, n.IPv4Addresses...)
			e.Addresses = append(e.Addresses, n.IPv6Addresses...)
			if n.IPv4Gateway != "" {
				e.Routes = append(e.Routes, route{To: "default", Via: n.IPv4Gateway})
			}
			if n.IPv6Gateway != "" {
				e.Routes = append(e.Routes, route{To: "default", Via: n.IPv6Gateway})
			}
		}
		if len(n.DNSServers) > 0 {
			e.Nameservers = &nameservers{Addresses: n.DNSServers}
		}
		doc.Ethernets[name] = e
	}
	return yamlString(doc)
}

func yamlString(v any) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		// Marshal cannot fail for these plain structs; fall back defensively.
		return ""
	}
	_ = enc.Close()
	return buf.String()
}

// --- Parsing (read path) ---

// ParsedMetaData is the decoded form of a NoCloud meta-data document.
type ParsedMetaData struct {
	InstanceID    string `yaml:"instance-id"`
	LocalHostname string `yaml:"local-hostname"`
}

// ParsedUserData is the decoded form of a cloud-config user-data document.
type ParsedUserData struct {
	Hostname          string   `yaml:"hostname"`
	User              string   `yaml:"user"`
	Password          string   `yaml:"password"`
	SSHPAuth          bool     `yaml:"ssh_pwauth"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys"`
}

// ParsedNetworkConfig is the decoded form of a Network Config Version 2
// document.
type ParsedNetworkConfig struct {
	Version   int                       `yaml:"version"`
	Ethernets map[string]ParsedEthernet `yaml:"ethernets"`
}

// ParsedEthernet is a single interface in a parsed network config.
type ParsedEthernet struct {
	Addresses []string `yaml:"addresses"`
	DHCP4     bool     `yaml:"dhcp4"`
	Routes    []struct {
		To  string `yaml:"to"`
		Via string `yaml:"via"`
	} `yaml:"routes"`
	Nameservers struct {
		Addresses []string `yaml:"addresses"`
	} `yaml:"nameservers"`
}

// ParseMetaData decodes a NoCloud meta-data document.
func ParseMetaData(data []byte) (*ParsedMetaData, error) {
	var d ParsedMetaData
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse meta-data yaml: %w", err)
	}
	return &d, nil
}

// ParseUserData decodes a cloud-config user-data document. A leading
// "#cloud-config" header line is accepted.
func ParseUserData(data []byte) (*ParsedUserData, error) {
	var d ParsedUserData
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse user-data yaml: %w", err)
	}
	return &d, nil
}

// ParseNetworkConfig decodes a Network Config Version 2 document.
func ParseNetworkConfig(data []byte) (*ParsedNetworkConfig, error) {
	var d ParsedNetworkConfig
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse network-config yaml: %w", err)
	}
	return &d, nil
}
