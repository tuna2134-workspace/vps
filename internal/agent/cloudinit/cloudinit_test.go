package cloudinit

import (
	"os"
	"strings"
	"testing"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/filesystem"
)

func TestGenerateProducesValidISO(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cidata.iso"
	workDir := dir + "/work"
	cfg := Config{
		Metadata: InstanceMetadata{InstanceID: "i-123", Hostname: "myhost"},
		User:     "root",
		Password: "secretpw",
		SSHKeys:  []string{"ssh-ed25519 AAAA... user@host"},
		Networks: []Network{{
			InterfaceIndex: 0,
			IPv4Addresses:  []string{"192.0.2.10/24"},
			IPv4Gateway:    "192.0.2.1",
			IPv6Addresses:  []string{"2001:db8::10/64"},
			IPv6Gateway:    "2001:db8::1",
			DNSServers:     []string{"1.1.1.1", "2606:4700:4700::1111"},
		}},
	}

	if err := Generate(path, cfg, workDir); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("iso not created: %v", err)
	}

	// The ISO must be readable by go-diskfs and contain the seed files.
	theDisk, err := diskfs.Open(path, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		t.Fatalf("open generated iso: %v", err)
	}
	defer theDisk.Close()

	fs, err := theDisk.GetFilesystem(0)
	if err != nil {
		t.Fatalf("get filesystem: %v", err)
	}
	if fs.Type() != filesystem.TypeISO9660 {
		t.Errorf("expected iso9660, got %v", fs.Type())
	}

	assertFileContains(t, fs, "/meta-data", "instance-id: i-123")
	assertFileContains(t, fs, "/meta-data", "local-hostname: myhost")
	assertFileContains(t, fs, "/user-data", "password: secretpw")
	assertFileContains(t, fs, "/user-data", "ssh_authorized_keys")
	assertFileContains(t, fs, "/network-config", "version: 2")
}

func assertFileContains(t *testing.T, fs filesystem.FileSystem, name, want string) {
	t.Helper()
	f, err := fs.OpenFile(name, os.O_RDONLY)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	n, _ := f.Read(buf)
	content := string(buf[:n])
	if !strings.Contains(content, want) {
		t.Errorf("%s does not contain %q:\n%s", name, want, content)
	}
}

func TestRenderNetworkConfigIPv4Static(t *testing.T) {
	cfg := Config{Networks: []Network{{
		InterfaceIndex: 0,
		IPv4Addresses:  []string{"192.0.2.10/24"},
		IPv4Gateway:    "192.0.2.1",
		DNSServers:     []string{"1.1.1.1"},
	}}}
	out := renderNetworkConfig(cfg)
	for _, want := range []string{"version: 2", "eth0:", "addresses:", "192.0.2.10/24", "routes:", "via: 192.0.2.1", "nameservers:", "1.1.1.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("network-config missing %q:\n%s", want, out)
		}
	}
}

func TestRenderNetworkConfigIPv6Static(t *testing.T) {
	cfg := Config{Networks: []Network{{
		InterfaceIndex: 0,
		IPv6Addresses:  []string{"2001:db8::10/64"},
		IPv6Gateway:    "2001:db8::1",
	}}}
	out := renderNetworkConfig(cfg)
	for _, want := range []string{"2001:db8::10/64", "via: 2001:db8::1"} {
		if !strings.Contains(out, want) {
			t.Errorf("network-config missing %q:\n%s", want, out)
		}
	}
}

func TestRenderNetworkConfigDHCP(t *testing.T) {
	cfg := Config{Networks: []Network{{InterfaceIndex: 0}}}
	out := renderNetworkConfig(cfg)
	if !strings.Contains(out, "dhcp4: true") {
		t.Errorf("expected dhcp4, got:\n%s", out)
	}
}
