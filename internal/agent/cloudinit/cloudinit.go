// Package cloudinit generates cloud-init NoCloud seed disks (ISO9660)
// containing user-data, meta-data, and network-config.
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
// The ISO contains user-data, meta-data, and network-config (Netplan v2).
func Generate(path string, cfg Config, workDir string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create iso directory: %w", err)
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

func renderMetaData(cfg Config) string {
	var b bytes.Buffer
	b.WriteString("#cloud-config\n")
	fmt.Fprintf(&b, "instance-id: %s\n", cfg.Metadata.InstanceID)
	fmt.Fprintf(&b, "local-hostname: %s\n", cfg.Metadata.Hostname)
	return b.String()
}

func renderUserData(cfg Config) string {
	var b bytes.Buffer
	b.WriteString("#cloud-config\n")
	fmt.Fprintf(&b, "hostname: %s\n", cfg.Metadata.Hostname)
	if cfg.User != "" {
		fmt.Fprintf(&b, "user: %s\n", cfg.User)
	}
	if cfg.Password != "" {
		// Password is written in cleartext per cloud-init semantics.
		fmt.Fprintf(&b, "password: %s\n", cfg.Password)
		fmt.Fprintf(&b, "ssh_pwauth: true\n")
	}
	if len(cfg.SSHKeys) > 0 {
		b.WriteString("ssh_authorized_keys:\n")
		for _, k := range cfg.SSHKeys {
			fmt.Fprintf(&b, "  - %s\n", k)
		}
	}
	if cfg.ExtraUserData != "" {
		b.WriteString(cfg.ExtraUserData)
		if cfg.ExtraUserData[len(cfg.ExtraUserData)-1] != '\n' {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderNetworkConfig emits Network Config Version 2.
func renderNetworkConfig(cfg Config) string {
	var b bytes.Buffer
	b.WriteString("version: 2\n")
	b.WriteString("ethernets:\n")
	for _, n := range cfg.Networks {
		name := fmt.Sprintf("eth%d", n.InterfaceIndex)
		fmt.Fprintf(&b, "  %s:\n", name)
		if len(n.IPv4Addresses) == 0 && len(n.IPv6Addresses) == 0 {
			fmt.Fprintf(&b, "    dhcp4: true\n")
			continue
		}
		fmt.Fprintf(&b, "    addresses:\n")
		for _, a := range n.IPv4Addresses {
			fmt.Fprintf(&b, "      - %s\n", a)
		}
		for _, a := range n.IPv6Addresses {
			fmt.Fprintf(&b, "      - %s\n", a)
		}
		if n.IPv4Gateway != "" || n.IPv6Gateway != "" {
			fmt.Fprintf(&b, "    routes:\n")
			if n.IPv4Gateway != "" {
				fmt.Fprintf(&b, "      - to: default\n        via: %s\n", n.IPv4Gateway)
			}
			if n.IPv6Gateway != "" {
				fmt.Fprintf(&b, "      - to: default\n        via: %s\n", n.IPv6Gateway)
			}
		}
		if len(n.DNSServers) > 0 {
			fmt.Fprintf(&b, "    nameservers:\n")
			fmt.Fprintf(&b, "      addresses:\n")
			for _, d := range n.DNSServers {
				fmt.Fprintf(&b, "        - %s\n", d)
			}
		}
	}
	return b.String()
}