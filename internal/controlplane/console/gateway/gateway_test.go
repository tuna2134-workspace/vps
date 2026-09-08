package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/console"
	"github.com/tuna2134/vps/internal/controlplane/models"
)

// TestGatewayDialSerialUnixSocket verifies the gateway dials serial consoles
// over a Unix domain socket.
func TestGatewayDialSerialUnixSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "serial.sock")

	// Start a fake "serial console" listening on a Unix socket.
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Echo a prompt like a serial console would.
		_, _ = conn.Write([]byte("login: "))
		buf := make([]byte, 16)
		n, _ := conn.Read(buf)
		_, _ = conn.Write(append([]byte("echo:"), buf[:n]...))
	}()

	g := New(console.NewService(nil, nil, time.Minute, nil, "http://x"), slog.New(slog.NewTextHandler(io.Discard, nil)))

	tok := &models.ConsoleToken{
		ConsoleType: "serial",
		VMID:        "vm-1",
		Path:        sockPath,
	}
	conn, err := g.dial(context.Background(), tok)
	if err != nil {
		t.Fatalf("dial serial: %v", err)
	}
	defer conn.Close()

	// Read the prompt.
	buf := make([]byte, 32)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if string(buf[:n]) != "login: " {
		t.Errorf("unexpected prompt: %q", buf[:n])
	}

	// Send input and read the echo.
	if _, err := conn.Write([]byte("root\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf[:n]) != "echo:root\n" {
		t.Errorf("unexpected echo: %q", buf[:n])
	}
}

// TestGatewayDialVNCRejectsMissingEndpoint verifies TCP consoles require a
// host/port.
func TestGatewayDialVNCRejectsMissingEndpoint(t *testing.T) {
	g := New(console.NewService(nil, nil, time.Minute, nil, "http://x"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	tok := &models.ConsoleToken{ConsoleType: "vnc"}
	if _, err := g.dial(context.Background(), tok); err == nil {
		t.Error("expected error dialing vnc without host/port")
	}
}
