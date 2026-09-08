// Package real contains integration tests that run against a REAL libvirt +
// QEMU hypervisor. These tests need no database: they exercise the production
// libvirt Adapter directly (storage pools/volumes, domain lifecycle, and the
// official virDomainOpenConsole / virDomainOpenGraphicsFD APIs).
//
// Run against a user session (no root):
//
//	REAL_LIBVIRT=1 LIBVIRT_URI=qemu:///session go test ./tests/real/ -v
//
// Run against the system hypervisor (needs sudo/root):
//
//	sudo -E env REAL_LIBVIRT=1 LIBVIRT_URI=qemu:///system go test ./tests/real/ -v
package real

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	libvirt "libvirt.org/go/libvirt"
	libvirtxml "libvirt.org/go/libvirtxml"

	agentlibvirt "github.com/tuna2134/vps/internal/agent/libvirt"
)

// TestMain avoids any database dependency: real-hypervisor tests only touch
// libvirt/QEMU.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// worldTempDir creates a single-level world-traversable temp directory under
// /tmp. QEMU runs as a different user (e.g. `qemu`) under qemu:///system, so
// the default 0700 t.TempDir() (and its 0700 parent) cannot be traversed.
func worldTempDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestRealLibvirt exercises the REAL libvirt Adapter against a live
// hypervisor:
//   - connects, creates a storage pool + volume, uploads and resizes it
//   - boots a minimal kernel+initramfs VM (real QEMU)
//   - verifies the serial console via virDomainOpenConsole
//   - verifies the VNC graphics device via virDomainOpenGraphicsFD
//
// Skipped unless REAL_LIBVIRT=1 and LIBVIRT_URI point at a usable hypervisor.
func TestRealLibvirt(t *testing.T) {
	if os.Getenv("REAL_LIBVIRT") != "1" {
		t.Skip("set REAL_LIBVIRT=1 to run against a real hypervisor")
	}
	uri := os.Getenv("LIBVIRT_URI")
	if uri == "" {
		uri = "qemu:///system"
	}

	// 1. Connect to the real hypervisor.
	adapter := agentlibvirt.NewAdapter(uri)
	if err := adapter.Connect(); err != nil {
		t.Fatalf("connect to real libvirt %s: %v", uri, err)
	}
	defer adapter.Close()

	ni, err := adapter.GetNodeInfo()
	if err != nil {
		t.Fatalf("get node info: %v", err)
	}
	t.Logf("connected to real libvirt: hostname=%s cpus=%d memory=%d", ni.Hostname, ni.CPUs, ni.MemoryBytes)

	// 2. Create a real dir storage pool (official libvirt-go-xml).
	pool := fmt.Sprintf("vps-real-%d", time.Now().UnixNano()%100000)
	poolDir := worldTempDir(t, "vps-pool-")
	_ = os.Chmod(poolDir, 0o777)
	if err := createStoragePool(uri, pool, poolDir); err != nil {
		t.Fatalf("create storage pool: %v", err)
	}
	defer func() {
		if err := destroyStoragePool(uri, pool); err != nil {
			t.Logf("destroy pool: %v", err)
		}
	}()

	// 3. Create a real raw volume, upload bytes, and resize it.
	volName := "testvol"
	if _, err := adapter.CreateVolumeWithFormat(pool, volName, 4*1024*1024, "raw"); err != nil {
		t.Fatalf("create volume: %v", err)
	}
	data := bytes.Repeat([]byte("real-libvirt-storage "), 64)
	if err := os.WriteFile(poolDir+"/payload.bin", data, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := adapter.UploadVolumeFile(pool, volName, poolDir+"/payload.bin"); err != nil {
		t.Fatalf("upload volume: %v", err)
	}
	vi, err := adapter.GetVolume(pool, volName)
	if err != nil {
		t.Fatalf("get volume: %v", err)
	}
	t.Logf("volume %s: capacity=%d path=%s", vi.Name, vi.CapacityBytes, vi.Path)
	if vi.CapacityBytes != 4*1024*1024 {
		t.Errorf("volume capacity wrong: %d", vi.CapacityBytes)
	}
	if err := adapter.ResizeVolume(pool, volName, 8*1024*1024); err != nil {
		t.Fatalf("resize volume: %v", err)
	}
	vi2, err := adapter.GetVolume(pool, volName)
	if err != nil {
		t.Fatalf("get resized volume: %v", err)
	}
	if vi2.CapacityBytes != 8*1024*1024 {
		t.Errorf("resized volume capacity wrong: %d", vi2.CapacityBytes)
	}

	// 4. VM boot + console (requires QEMU installed).
	qemu := findQEMU(t)
	if qemu == "" {
		t.Skip("QEMU not installed on this host; install qemu to run the VM/console part")
	}
	kernel := os.Getenv("KERNEL_PATH")
	if kernel == "" {
		kernel = "/boot/vmlinuz-linux"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s not available: %v", kernel, err)
	}

	initramfs := buildInitramfs(t)
	kernel = ensureReadable(t, kernel)
	name := fmt.Sprintf("vps-real-%d", time.Now().UnixNano()%100000)
	xml, err := agentlibvirt.GenerateDomainXML(agentlibvirt.DomainConfig{
		Name:        name,
		MemoryBytes: 512 * 1024 * 1024,
		VCPU:        1,
		Kernel:      kernel,
		Initrd:      initramfs,
		Cmdline:     "console=ttyS0,115200n8",
		VNCPassword: "test",
		Emulator:    qemu,
		// The cloud-init seed volume attached as a SATA cdrom, mirroring the
		// manager's disk layout (validates libvirt accepts the sata cdrom).
		Disks: []agentlibvirt.DomainDisk{{
			Device:    "cdrom",
			Type:      "file",
			Source:    vi.Path,
			Driver:    "raw",
			TargetDev: "sda",
			Writable:  false,
		}},
	})
	if err != nil {
		t.Fatalf("generate domain xml: %v", err)
	}
	if err := adapter.DefineDomain(xml); err != nil {
		t.Fatalf("define domain: %v", err)
	}
	defer func() {
		_ = adapter.DestroyDomain(name)
		_ = adapter.UndefineDomain(name)
	}()

	if err := adapter.StartDomain(name); err != nil {
		t.Fatalf("start domain: %v", err)
	}
	waitForDomainState(t, adapter, name, agentlibvirt.StateRunning)

	// 5. Serial console via virDomainOpenConsole (the real code path).
	if err := testSerialConsole(adapter, name, t); err != nil {
		t.Fatal(err)
	}

	// 6. VNC graphics device via virDomainOpenGraphicsFD. QEMU's VNC server
	//    sends the RFB handshake greeting immediately on connect.
	testVNCHandshake(t, adapter, name)

	// 7. Best-effort graceful shutdown via the serial console; hard cleanup is
	//    handled by the deferred DestroyDomain/UndefineDomain above.
	if cs, err := adapter.OpenConsole(name); err == nil {
		_, _ = cs.Send([]byte("exit\n"))
		time.Sleep(2 * time.Second)
		_ = cs.Close()
	}
	t.Log("REAL LIBVIRT + QEMU VERIFIED (storage, serial console, VNC)")
}

// testSerialConsole opens the serial console and verifies data both ways.
func testSerialConsole(adapter *agentlibvirt.Adapter, name string, t *testing.T) error {
	t.Helper()
	cs, err := adapter.OpenConsole(name)
	if err != nil {
		return fmt.Errorf("open console: %w", err)
	}
	defer cs.Close()

	banner, err := readConsoleUntil(cs, "console ready", 60*time.Second)
	if err != nil {
		return fmt.Errorf("read banner: %w", err)
	}
	t.Logf("guest banner: %q", banner)
	if !bytes.Contains([]byte(banner), []byte("VPS real-libvirt console ready")) {
		return fmt.Errorf("unexpected banner: %q", banner)
	}

	if _, err := cs.Send([]byte("hello-libvirt\n")); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	echo, err := readConsoleUntil(cs, "console-echo:hello-libvirt", 30*time.Second)
	if err != nil {
		return fmt.Errorf("read echo: %w", err)
	}
	t.Logf("guest echo: %q", echo)
	if !bytes.Contains([]byte(echo), []byte("console-echo:hello-libvirt")) {
		return fmt.Errorf("unexpected echo: %q", echo)
	}
	return nil
}

// testVNCHandshake connects via virDomainOpenGraphicsFD and expects the RFB
// greeting ("RFB 003.008").
func testVNCHandshake(t *testing.T, adapter *agentlibvirt.Adapter, name string) {
	t.Helper()
	vs, err := adapter.OpenGraphics(name)
	if err != nil {
		t.Fatalf("open graphics: %v", err)
	}
	defer vs.Close()

	greeting, err := readConsoleUntil(vs, "RFB", 30*time.Second)
	if err != nil {
		t.Fatalf("read RFB greeting: %v", err)
	}
	t.Logf("vnc greeting: %q", greeting)
	if !bytes.Contains([]byte(greeting), []byte("RFB 003.008")) {
		t.Errorf("unexpected vnc greeting: %q", greeting)
	}
}

// TestRealVMShutdownReboot verifies VM shutdown and reboot lifecycle against a
// REAL QEMU guest. The guest init powers itself off / reboots in response to
// serial commands:
//
//	start -> running
//	graceful shutdown (serial "poweroff") -> guest powers off -> shutoff
//	start -> running
//	reboot (serial "reboot")              -> guest reboots -> running again
//	force stop (virDomainDestroy)         -> shutoff
//
// The libvirt virDomainShutdown / virDomainReboot calls are also asserted to
// be accepted (for ACPI-capable guests, e.g. real cloud images, they directly
// shut down / reboot the guest).
func TestRealVMShutdownReboot(t *testing.T) {
	if os.Getenv("REAL_LIBVIRT") != "1" {
		t.Skip("set REAL_LIBVIRT=1 to run against a real hypervisor")
	}
	uri := os.Getenv("LIBVIRT_URI")
	if uri == "" {
		uri = "qemu:///system"
	}
	qemu := findQEMU(t)
	if qemu == "" {
		t.Skip("QEMU not installed on this host")
	}
	kernel := os.Getenv("KERNEL_PATH")
	if kernel == "" {
		kernel = "/boot/vmlinuz-linux"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s not available: %v", kernel, err)
	}

	adapter := agentlibvirt.NewAdapter(uri)
	if err := adapter.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer adapter.Close()

	initramfs := buildInitramfs(t)
	name := fmt.Sprintf("vps-lc-%d", time.Now().UnixNano()%100000)
	xml, err := agentlibvirt.GenerateDomainXML(agentlibvirt.DomainConfig{
		Name:        name,
		MemoryBytes: 512 * 1024 * 1024,
		VCPU:        1,
		Kernel:      kernel,
		Initrd:      initramfs,
		Cmdline:     "console=ttyS0,115200n8",
		VNCPassword: "test",
		Emulator:    qemu,
	})
	if err != nil {
		t.Fatalf("generate domain xml: %v", err)
	}
	if err := adapter.DefineDomain(xml); err != nil {
		t.Fatalf("define domain: %v", err)
	}
	defer func() {
		_ = adapter.DestroyDomain(name)
		_ = adapter.UndefineDomain(name)
	}()

	if err := adapter.StartDomain(name); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForDomainState(t, adapter, name, agentlibvirt.StateRunning)
	t.Log("vm running")

	// 1. Graceful shutdown: the guest init powers itself off in response to
	//    the serial "poweroff" command; the domain must reach shutoff.
	cs, err := adapter.OpenConsole(name)
	if err != nil {
		t.Fatalf("open console: %v", err)
	}
	// Read the boot banner first so the console stream is stable and no input
	// bytes are lost during boot output flush.
	if _, err := readConsoleUntil(cs, "console ready", 30*time.Second); err != nil {
		_ = cs.Close()
		t.Fatalf("read boot banner: %v", err)
	}
	if _, err := cs.Send([]byte("poweroff\n")); err != nil {
		_ = cs.Close()
		t.Fatalf("send poweroff: %v", err)
	}
	startShutdown := time.Now()
	waitForDomainState(t, adapter, name, agentlibvirt.StateShutoff)
	_ = cs.Close()
	t.Logf("graceful shutdown verified -> shutoff (took %s)", time.Since(startShutdown))

	// 2. Start again.
	if err := adapter.StartDomain(name); err != nil {
		t.Fatalf("start after shutdown: %v", err)
	}
	waitForDomainState(t, adapter, name, agentlibvirt.StateRunning)
	t.Log("restarted after shutdown")

	// 3. Reboot: the guest reboots on the serial "reboot" command; the serial
	//    console comes back with the boot banner.
	rcs, err := adapter.OpenConsole(name)
	if err != nil {
		t.Fatalf("open console for reboot: %v", err)
	}
	if _, err := readConsoleUntil(rcs, "console ready", 30*time.Second); err != nil {
		_ = rcs.Close()
		t.Fatalf("read banner before reboot: %v", err)
	}
	if _, err := rcs.Send([]byte("reboot\n")); err != nil {
		_ = rcs.Close()
		t.Fatalf("send reboot: %v", err)
	}
	_ = rcs.Close()
	if err := waitForRebootBanner(adapter, name, 90*time.Second); err != nil {
		t.Fatalf("reboot verification: %v", err)
	}
	waitForDomainState(t, adapter, name, agentlibvirt.StateRunning)
	t.Log("reboot verified (guest rebooted and serial console is back)")

	// The libvirt virDomainShutdown / virDomainReboot APIs are accepted
	// (they are what the agent's stop/reboot operations call); for
	// ACPI-capable guests (real cloud images) they power off / reboot the
	// guest directly.
	if err := adapter.ShutdownDomain(name); err != nil {
		t.Fatalf("shutdown (libvirt): %v", err)
	}
	if err := adapter.RebootDomain(name); err != nil {
		t.Fatalf("reboot (libvirt): %v", err)
	}
	t.Log("libvirt shutdown/reboot API accepted")

	// 4. Force stop.
	if err := adapter.DestroyDomain(name); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	waitForDomainState(t, adapter, name, agentlibvirt.StateShutoff)
	t.Log("force stop verified (destroy) -> shutoff")
}

// waitForRebootBanner waits for the domain to return to running and the guest
// serial banner to appear again (indicating a completed reboot).
func waitForRebootBanner(adapter *agentlibvirt.Adapter, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, _ := adapter.GetDomainState(name)
		if state != agentlibvirt.StateRunning {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		cs, err := adapter.OpenConsole(name)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		banner, err := readConsoleUntil(cs, "console ready", 15*time.Second)
		_ = cs.Close()
		if err == nil && bytes.Contains([]byte(banner), []byte("VPS real-libvirt console ready")) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("guest did not reboot within %s", timeout)
}

// findQEMU locates a usable QEMU binary.
func findQEMU(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/usr/bin/qemu-system-x86_64", "/usr/libexec/qemu-kvm", "/usr/bin/qemu-kvm"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// createStoragePool defines and starts a real dir storage pool.
func createStoragePool(uri, name, dir string) error {
	conn, err := libvirt.NewConnect(uri)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Render the pool XML with the official libvirt-go-xml bindings.
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

// destroyStoragePool tears down and undefines a pool.
func destroyStoragePool(uri, name string) error {
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

// waitForDomainState polls until the domain reaches the expected state.
func waitForDomainState(t *testing.T, adapter *agentlibvirt.Adapter, name string, want agentlibvirt.VMState) {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		state, err := adapter.GetDomainState(name)
		if err == nil && state == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	state, _ := adapter.GetDomainState(name)
	t.Fatalf("domain %s did not reach %s (got %s)", name, want, state)
}

// readConsoleUntil reads from the console stream until the marker appears or
// the timeout elapses.
func readConsoleUntil(cs agentlibvirt.ConsoleStream, marker string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var out []byte
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		n, err := cs.Recv(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if bytes.Contains(out, []byte(marker)) {
				return string(out), nil
			}
		}
		if err != nil {
			return string(out), err
		}
	}
	return string(out), fmt.Errorf("timeout waiting for %q; got %q", marker, out)
}

// ensureReadable returns a copy of the file in a world-readable location when
// the original is not readable by other users (QEMU runs as `qemu` under
// qemu:///system).
func ensureReadable(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm()&0o004 != 0 && info.Mode().Perm()&0o001 != 0 {
		return path
	}
	dir := worldTempDir(t, "vps-kernel-")
	dst := filepath.Join(dir, filepath.Base(path))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}
	t.Logf("copied %s -> %s (world-readable)", path, dst)
	return dst
}

// buildInitramfs compiles the static init and packs it into a gzip cpio
// initramfs. Files are made world-readable because QEMU runs as a different
// user (e.g. `qemu`) under qemu:///system.
func buildInitramfs(t *testing.T) string {
	t.Helper()
	dir := worldTempDir(t, "vps-initramfs-")
	root := filepath.Join(dir, "root")
	for _, d := range []string{"proc", "sys", "dev", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	initBin := filepath.Join(dir, "init")
	build := exec.Command("go", "build", "-o", initBin, "./tests/real/testinit")
	build.Dir = "../.." // module root (tests run from tests/real)
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
	return initramfs
}
