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

// TestGenerateProducesParseableYAML verifies the generated files are valid
// YAML that round-trips through the parsers.
func TestGenerateProducesParseableYAML(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cidata.iso"
	cfg := Config{
		Metadata: InstanceMetadata{InstanceID: "i-456", Hostname: "parsehost"},
		User:     "root",
		Password: "s3cret",
		SSHKeys:  []string{"ssh-ed25519 AAAA user@host"},
		Networks: []Network{{
			InterfaceIndex: 0,
			IPv4Addresses:  []string{"198.51.100.10/24"},
			IPv4Gateway:    "198.51.100.1",
			DNSServers:     []string{"9.9.9.9"},
		}},
	}
	if err := Generate(path, cfg, dir+"/work"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	theDisk, err := diskfs.Open(path, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		t.Fatalf("open iso: %v", err)
	}
	defer theDisk.Close()
	fs, err := theDisk.GetFilesystem(0)
	if err != nil {
		t.Fatalf("get filesystem: %v", err)
	}

	// meta-data
	md, err := readFileBytes(fs, "/meta-data")
	if err != nil {
		t.Fatalf("read meta-data: %v", err)
	}
	parsedMD, err := ParseMetaData(md)
	if err != nil {
		t.Fatalf("parse meta-data: %v\n%s", err, md)
	}
	if parsedMD.InstanceID != "i-456" || parsedMD.LocalHostname != "parsehost" {
		t.Errorf("meta-data parsed wrong: %+v", parsedMD)
	}

	// user-data
	ud, err := readFileBytes(fs, "/user-data")
	if err != nil {
		t.Fatalf("read user-data: %v", err)
	}
	if !strings.HasPrefix(string(ud), "#cloud-config\n") {
		t.Errorf("user-data missing #cloud-config header:\n%s", ud)
	}
	parsedUD, err := ParseUserData(ud)
	if err != nil {
		t.Fatalf("parse user-data: %v\n%s", err, ud)
	}
	if parsedUD.User != "root" || parsedUD.Password != "s3cret" || !parsedUD.SSHPAuth {
		t.Errorf("user-data parsed wrong: %+v", parsedUD)
	}
	if len(parsedUD.SSHAuthorizedKeys) != 1 {
		t.Errorf("ssh keys parsed wrong: %+v", parsedUD.SSHAuthorizedKeys)
	}

	// network-config
	nc, err := readFileBytes(fs, "/network-config")
	if err != nil {
		t.Fatalf("read network-config: %v", err)
	}
	parsedNC, err := ParseNetworkConfig(nc)
	if err != nil {
		t.Fatalf("parse network-config: %v\n%s", err, nc)
	}
	if parsedNC.Version != 2 {
		t.Errorf("network-config version wrong: %d", parsedNC.Version)
	}
	eth0, ok := parsedNC.Ethernets["eth0"]
	if !ok {
		t.Fatalf("no eth0 in network-config: %+v", parsedNC)
	}
	if len(eth0.Addresses) != 1 || eth0.Addresses[0] != "198.51.100.10/24" {
		t.Errorf("addresses wrong: %+v", eth0.Addresses)
	}
	if len(eth0.Routes) != 1 || eth0.Routes[0].Via != "198.51.100.1" {
		t.Errorf("routes wrong: %+v", eth0.Routes)
	}
	if len(eth0.Nameservers.Addresses) != 1 || eth0.Nameservers.Addresses[0] != "9.9.9.9" {
		t.Errorf("dns wrong: %+v", eth0.Nameservers)
	}
}

func readFileBytes(fs filesystem.FileSystem, name string) ([]byte, error) {
	f, err := fs.OpenFile(name, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	return buf[:n], nil
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
