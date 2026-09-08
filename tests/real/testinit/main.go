// Command testinit builds a minimal static initramfs init that brings up a
// serial console shell (used by the real-libvirt E2E test to boot a VM that
// has no OS installed).
package main

import (
	"fmt"
	"os"
	"syscall"
)

func main() {
	_ = syscall.Mount("proc", "/proc", "proc", 0, "")
	_ = syscall.Mount("sysfs", "/sys", "sysfs", 0, "")
	_ = syscall.Mount("devtmpfs", "/dev", "devtmpfs", 0, "")

	// Attach the serial console (ttyS0) as stdin/stdout/stderr.
	if f, err := os.OpenFile("/dev/console", os.O_RDWR, 0); err == nil {
		os.Stdin = f
		os.Stdout = f
		os.Stderr = f
	}

	fmt.Fprintf(os.Stdout, "\nVPS real-libvirt console ready (init pid %d)\n", os.Getpid())

	buf := make([]byte, 512)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			break
		}
		line := string(buf[:n])
		if line == "exit\n" || line == "exit" {
			break
		}
		fmt.Fprintf(os.Stdout, "console-echo:%s", line)
	}

	_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
}
