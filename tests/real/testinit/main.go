// Command testinit builds a minimal static initramfs init that brings up a
// serial console shell (used by the real-libvirt E2E tests to boot a VM that
// has no OS installed). It supports graceful shutdown/reboot:
//
//	serial "poweroff" or ACPI power button -> LINUX_REBOOT_CMD_POWER_OFF
//	serial "reboot"                        -> LINUX_REBOOT_CMD_RESTART
//	other serial input                     -> "console-echo:<line>"
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	evKey        = 0x01
	keyPower     = 116
	keyPress     = 1
	inputEventSz = 24 // struct input_event (timeval 16 + type 2 + code 2 + value 4)
)

func main() {
	_ = syscall.Mount("proc", "/proc", "proc", 0, "")
	_ = syscall.Mount("sysfs", "/sys", "sysfs", 0, "")
	_ = syscall.Mount("devtmpfs", "/dev", "devtmpfs", 0, "")

	// Attach the serial console (ttyS0) as stdin/stdout/stderr.
	var console *os.File
	if f, err := os.OpenFile("/dev/console", os.O_RDWR, 0); err == nil {
		console = f
		os.Stdin = f
		os.Stdout = f
		os.Stderr = f
	}

	// Handle the ACPI power button so virDomainShutdown works.
	go watchPowerButton()

	fmt.Fprintf(os.Stdout, "\nVPS real-libvirt console ready (init pid %d)\n", os.Getpid())

	buf := make([]byte, 512)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			break
		}
		line := string(buf[:n])
		switch line {
		case "poweroff\n", "poweroff", "exit\n", "exit":
			powerOff()
		case "reboot\n", "reboot":
			reboot()
		default:
			fmt.Fprintf(os.Stdout, "console-echo:%s", line)
		}
	}
	_ = console
	powerOff()
}

// watchPowerButton polls /dev/input/event* for the ACPI power button (KEY_POWER)
// and powers the guest off gracefully when pressed.
func watchPowerButton() {
	devs, _ := filepath.Glob("/dev/input/event*")
	for _, dev := range devs {
		f, err := os.OpenFile(dev, os.O_RDONLY, 0)
		if err != nil {
			continue
		}
		go readInput(f)
	}
}

func readInput(f *os.File) {
	defer f.Close()
	buf := make([]byte, inputEventSz)
	for {
		n, err := f.Read(buf)
		if err != nil || n < inputEventSz {
			return
		}
		evType := binary.LittleEndian.Uint16(buf[16:18])
		code := binary.LittleEndian.Uint16(buf[18:20])
		value := binary.LittleEndian.Uint32(buf[20:24])
		if evType == evKey && code == keyPower && value == keyPress {
			powerOff()
			return
		}
	}
}

func powerOff() {
	fmt.Fprintf(os.Stdout, "powering off\n")
	_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
	for {
		_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
	}
}

func reboot() {
	fmt.Fprintf(os.Stdout, "rebooting\n")
	_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	for {
		_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	}
}
