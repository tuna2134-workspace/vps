package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/coder/websocket"

	"github.com/tuna2134/vps/internal/controlplane/console"
	consolegateway "github.com/tuna2134/vps/internal/controlplane/console/gateway"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/users"
	"github.com/tuna2134/vps/internal/controlplane/vms"
)

// realPtyConsole allocates a real pseudo-terminal and runs a fake serial
// console on it. The pty slave path is what libvirt would report as the
// serial console device.
func realPtyConsole(t *testing.T) (slavePath string, stop func()) {
	t.Helper()
	master, slave, err := unix.Openpty()
	if err != nil {
		t.Fatalf("openpty: %v", err)
	}
	// The slave path is a /dev/pts/N device file; the gateway connects to it
	// with net.Dial("unix", slavePath).
	slavePath = fmt.Sprintf("/dev/pts/%d", slave)

	var wg sync.WaitGroup
	stopCh := make(chan struct{})
	stop = func() {
		close(stopCh)
		_ = unix.Close(master)
		_ = unix.Close(slave)
		wg.Wait()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Write a login banner to the pty.
		_, _ = unix.Write(master, []byte("vps serial console ready\nlogin: "))
		buf := make([]byte, 512)
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			n, err := unix.Read(master, buf)
			if err != nil {
				return
			}
			line := string(buf[:n])
			if line == "echo-banner" {
				_, _ = unix.Write(master, []byte("serial-echo-ok\n"))
			} else if line == "root" {
				_, _ = unix.Write(master, []byte("Password: "))
			} else {
				_, _ = unix.Write(master, []byte(fmt.Sprintf("got:%s\n", line)))
			}
		}
	}()

	// Give the pty time to become readable.
	time.Sleep(100 * time.Millisecond)
	return slavePath, stop
}

// TestSerialConsoleConnection builds a VM through the real agent provisioning
// flow, then connects to its serial console through the WebSocket gateway and
// verifies data actually flows both ways over a real PTY.
func TestSerialConsoleConnection(t *testing.T) {
	ctx := context.Background()
	te := setupE2E(t)

	// Register a user.
	email := fmt.Sprintf("serial-%d@example.com", time.Now().UnixNano())
	user, err := te.userSvc.Register(ctx, users.RegistrationRequest{
		Email: email, Password: "password123",
		FirstName: "S", LastName: "Test", LegalName: "S Test",
		Country: "JP", PostalCode: "100-0001", State: "Tokyo", City: "Chiyoda", AddressLine1: "1-1",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Plan / network / image.
	uniq := suffix()
	plan, err := te.planSvc.Create(ctx, &models.Plan{
		Name: "serial-plan-" + uniq, VCPU: 1, MemoryMB: 1024, DiskGB: 10, MonthlyPriceCents: 500, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	net, err := te.netSvc.CreateNetwork(ctx, &models.Network{
		Name: "serial-net-" + uniq, Bridge: "br-serial", DNS1: "1.1.1.1", DNS2: "1.0.0.1",
	})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	if _, err := te.netSvc.AddPool(ctx, &models.IPPool{NetworkID: net.ID, CIDR: "203.0.113.0/28", Type: "ipv4", Gateway: "203.0.113.1"}); err != nil {
		t.Fatalf("add pool: %v", err)
	}
	imagePath := os.TempDir() + "/serial-img-" + uniq + ".qcow2"
	if err := os.WriteFile(imagePath, make([]byte, 1024), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	img, err := pool.Images.Create(ctx, &models.Image{
		Name: "ubuntu", Version: "serial-" + uniq, Format: "qcow2", SourceURL: imagePath, CloudInitCompatible: true,
	})
	if err != nil {
		t.Fatalf("create image: %v", err)
	}

	// Build the VM through the real agent provisioning flow.
	vm, op, err := te.vmSvc.CreateVM(ctx, user.ID, vms.CreateRequest{
		PlanID: plan.ID, NetworkID: net.ID, ImageID: img.ID, Name: "serial-vm", Hostname: "serial-vm",
	}, "serial-key-"+uniq)
	if err != nil {
		t.Fatalf("create vm: %v", err)
	}
	finished, err := te.vmSvc.WaitForOperation(ctx, op.ID)
	if err != nil || finished.Status != models.OperationSucceeded {
		t.Fatalf("provisioning failed: %v status=%s err=%s", err, finished.Status, finished.Error)
	}

	// Point the agent's serial console at a real PTY.
	ptyPath, stopPty := realPtyConsole(t)
	defer stopPty()
	te.agentFake.SetSerialInfo(ptyPath)

	// The VM must be running for a console token.
	running, _ := pool.VMs.GetByID(ctx, vm.ID)
	if running.Status != models.VMStatusRunning {
		t.Fatalf("vm not running: %s", running.Status)
	}

	// Issue a serial console token through the Control Plane console service.
	tok, rawToken, err := te.consoleSvc.Issue(ctx, user.ID, vm.ID, vm.Name, te.agentAddr, console.TypeSerial)
	if err != nil {
		t.Fatalf("issue serial token: %v", err)
	}
	if tok.Path != ptyPath {
		t.Fatalf("expected serial path %s, got %s", ptyPath, tok.Path)
	}

	// Serve the gateway over httptest and connect a WebSocket client.
	gateway := consolegateway.New(te.consoleSvc, log)
	srv := httptest.NewServer(gateway.HandleWS)
	defer srv.Close()

	wsURL := "ws" + fmt.Sprintf("%s/console/ws?token=%s", srv.URL[len("http"):], rawToken)
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: &http.Client{Timeout: 5 * time.Second}})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "done")

	// Read the serial banner + prompt from the pty through the gateway.
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read banner: %v", err)
	}
	banner := string(data)
	if banner != "vps serial console ready\nlogin: " {
		t.Errorf("unexpected banner: %q", banner)
	}

	// Send "root" and expect the Password prompt.
	if err := ws.Write(ctx, websocket.MessageText, []byte("root")); err != nil {
		t.Fatalf("write root: %v", err)
	}
	_, data, err = ws.Read(ctx)
	if err != nil {
		t.Fatalf("read password prompt: %v", err)
	}
	if string(data) != "Password: " {
		t.Errorf("unexpected password prompt: %q", data)
	}

	// Send a command and verify the pty console's echo reaches the client.
	if err := ws.Write(ctx, websocket.MessageText, []byte("echo-banner")); err != nil {
		t.Fatalf("write command: %v", err)
	}
	_, data, err = ws.Read(ctx)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(data) != "serial-echo-ok\n" {
		t.Errorf("unexpected echo: %q", data)
	}

	t.Logf("serial console verified: %q -> root -> %q -> echo-banner -> %q", banner, "Password: ", string(data))

	// Token reuse must be rejected with 401.
	ws2, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		ws2.Close(websocket.StatusNormalClosure, "done")
		t.Error("expected reused token to be rejected")
	} else {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			t.Logf("token reuse correctly rejected with %d", resp.StatusCode)
		} else {
			t.Errorf("token reuse rejection status: %v", err)
		}
	}
}