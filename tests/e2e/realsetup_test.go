package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/tuna2134/vps/internal/agent/grpcserver"
	"github.com/tuna2134/vps/internal/agent/image"
	agentlibvirt "github.com/tuna2134/vps/internal/agent/libvirt"
	"github.com/tuna2134/vps/internal/agent/manager"
	"github.com/tuna2134/vps/internal/agent/metrics"
	"github.com/tuna2134/vps/internal/agent/storage"

	libvirt "libvirt.org/go/libvirt"
	libvirtxml "libvirt.org/go/libvirtxml"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
)

// realLibvirtEnv wires a REAL libvirt agent: a live libvirt connection, a dir
// storage pool, a real bridge, and a minimal kernel+initramfs image so the
// provisioned VM actually boots.
type realLibvirtEnv struct {
	adapter     *agentlibvirt.Adapter
	poolName    string
	bridgeName  string
	workDir     string
	kernelPath  string
	initrdPath  string
	mgr         *manager.Manager
	agentServer *grpcserver.Server
	agentAddr   string
}

// newRealLibvirtEnv sets up a real hypervisor test environment. Requires
// REAL_LIBVIRT=1 and a usable LIBVIRT_URI (qemu:///system under sudo, or
// qemu:///session without root).
func newRealLibvirtEnv(t *testing.T) *realLibvirtEnv {
	t.Helper()
	if os.Getenv("REAL_LIBVIRT") != "1" {
		t.Skip("set REAL_LIBVIRT=1 to run against a real hypervisor")
	}
	uri := os.Getenv("LIBVIRT_URI")
	if uri == "" {
		uri = "qemu:///system"
	}

	adapter := agentlibvirt.NewAdapter(uri)
	if err := adapter.Connect(); err != nil {
		t.Fatalf("connect to real libvirt %s: %v", uri, err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	uniq := fmt.Sprintf("%d", time.Now().UnixNano()%100000)
	env := &realLibvirtEnv{
		adapter:    adapter,
		poolName:   "vps-pool-" + uniq,
		bridgeName: "vps-br-" + uniq,
		workDir:    "/tmp/vps-agent-" + uniq,
	}

	// World-readable work dir (QEMU runs as a different user).
	if err := os.MkdirAll(env.workDir, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(env.workDir) })

	// Storage pool (official libvirt-go-xml).
	poolDir := "/tmp/vps-pool-" + uniq
	if err := os.MkdirAll(poolDir, 0o777); err != nil {
		t.Fatalf("mkdir pool: %v", err)
	}
	if err := createPool(uri, env.poolName, poolDir); err != nil {
		t.Fatalf("create storage pool: %v", err)
	}
	t.Cleanup(func() { _ = destroyPool(uri, env.poolName) })

	// Real bridge (requires root; works under sudo with qemu:///system).
	if uri == "qemu:///system" {
		if err := createBridge(env.bridgeName); err != nil {
			t.Fatalf("create bridge: %v", err)
		}
		t.Cleanup(func() { _ = deleteBridge(env.bridgeName) })
	}

	// Kernel + minimal initramfs for direct kernel boot.
	kernel := os.Getenv("KERNEL_PATH")
	if kernel == "" {
		kernel = "/boot/vmlinuz-linux"
	}
	env.kernelPath = ensureReadable(t, kernel)
	env.initrdPath = buildInitramfs(t)

	// Wire the REAL agent.
	store := storage.New(adapter)
	mgr := manager.New(adapter, store, nil, image.New(env.workDir), env.workDir, env.poolName, log)
	metricsProvider := metrics.NewProvider(adapter)
	env.mgr = mgr
	env.agentServer = grpcserver.New(mgr, adapter, metricsProvider, nil, log)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("agent listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	agentv1.RegisterAgentServiceServer(grpcSrv, env.agentServer)
	go grpcSrv.Serve(lis)
	t.Cleanup(grpcSrv.Stop)
	env.agentAddr = lis.Addr().String()

	return env
}

// kernelImage returns the Image fields for a kernel-boot image.
func (e *realLibvirtEnv) kernelImage(name, version string) (kernelURL, initrdURL, cmdline string) {
	return e.kernelPath, e.initrdPath, "console=ttyS0,115200n8"
}

// createPool defines and starts a real dir storage pool.
func createPool(uri, name, dir string) error {
	conn, err := libvirt.NewConnect(uri)
	if err != nil {
		return err
	}
	defer conn.Close()
	poolDoc := &libvirtxml.StoragePool{
		Type: "dir",
		Name: name,
		Target: &libvirtxml.StoragePoolTarget{
			Path: dir,
		},
	}
	xml, err := poolDoc.Marshal()
	if err != nil {
		return err
	}
	poolObj, err := conn.StoragePoolDefineXML(xml, 0)
	if err != nil {
		return err
	}
	defer poolObj.Free()
	return poolObj.Create(0)
}

func destroyPool(uri, name string) error {
	conn, err := libvirt.NewConnect(uri)
	if err != nil {
		return err
	}
	defer conn.Close()
	poolObj, err := conn.LookupStoragePoolByName(name)
	if err != nil {
		return err
	}
	defer poolObj.Free()
	_ = poolObj.Destroy()
	return poolObj.Undefine()
}

func createBridge(name string) error {
	if out, err := exec.Command("ip", "link", "add", name, "type", "bridge").CombinedOutput(); err != nil {
		return fmt.Errorf("ip link add %s: %v: %s", name, err, out)
	}
	if out, err := exec.Command("ip", "link", "set", name, "up").CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set %s up: %v: %s", name, err, out)
	}
	return nil
}

func deleteBridge(name string) error {
	return exec.Command("ip", "link", "del", name).Run()
}

// ensureReadable copies a file to a world-readable location when needed.
func ensureReadable(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm()&0o044 == 0o044 {
		return path
	}
	dst := filepath.Join("/tmp", "vps-kernel-"+fmt.Sprint(time.Now().UnixNano()))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(dst) })
	return dst
}

// buildInitramfs compiles the static init and packs it into a world-readable
// gzip cpio initramfs.
func buildInitramfs(t *testing.T) string {
	t.Helper()
	dir := "/tmp/vps-initramfs-" + fmt.Sprint(time.Now().UnixNano())
	root := filepath.Join(dir, "root")
	for _, d := range []string{"proc", "sys", "dev", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	initBin := filepath.Join(dir, "init")
	build := exec.Command("go", "build", "-o", initBin, "./tests/real/testinit")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build init: %v\n%s", err, out)
	}
	if err := os.Rename(initBin, filepath.Join(root, "init")); err != nil {
		t.Fatalf("copy init: %v", err)
	}
	initramfs := filepath.Join(dir, "initramfs.cpio.gz")
	cmd := exec.Command("sh", "-c", "find . -print0 | cpio --null -o --format=newc | gzip > "+initramfs)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pack initramfs: %v\n%s", err, out)
	}
	if err := os.Chmod(initramfs, 0o644); err != nil {
		t.Fatalf("chmod initramfs: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return initramfs
}
