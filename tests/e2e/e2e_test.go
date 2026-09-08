// Package e2e exercises the full provisioning flow against real PostgreSQL
// and a real gRPC agent backed by a real libvirt + QEMU hypervisor. Requires
// REAL_LIBVIRT=1 and a usable LIBVIRT_URI (run with sudo for qemu:///system).
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tuna2134/vps/internal/agent/grpcserver"
	agentlibvirt "github.com/tuna2134/vps/internal/agent/libvirt"
	consolegateway "github.com/tuna2134/vps/internal/controlplane/console/gateway"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tuna2134/vps/internal/controlplane/agents"
	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/auth"
	"github.com/tuna2134/vps/internal/controlplane/billing"
	"github.com/tuna2134/vps/internal/controlplane/cluster"
	"github.com/tuna2134/vps/internal/controlplane/console"
	"github.com/tuna2134/vps/internal/controlplane/database"
	"github.com/tuna2134/vps/internal/controlplane/grpcclient"
	"github.com/tuna2134/vps/internal/controlplane/macalloc"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/networks"
	"github.com/tuna2134/vps/internal/controlplane/plans"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
	"github.com/tuna2134/vps/internal/controlplane/scheduler"
	"github.com/tuna2134/vps/internal/controlplane/users"
	"github.com/tuna2134/vps/internal/controlplane/vms"
	"github.com/tuna2134/vps/migrations"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
)

const defaultDSN = "postgres://postgres:postgres@localhost:5432/vps_e2e?sslmode=disable"

var (
	pool *repositories.Repositories
	dsn  string
	log  *slog.Logger
)

func TestMain(m *testing.M) {
	dsn = os.Getenv("E2E_DATABASE_URL")
	if dsn == "" {
		dsn = defaultDSN
	}
	log = slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := migrations.EnsureDatabase(ctx, dsn); err != nil {
		panic(err)
	}
	if err := migrations.WaitForDB(ctx, dsn, log, time.Second); err != nil {
		panic(err)
	}
	runner, err := migrations.New(ctx, dsn, log)
	if err != nil {
		panic(err)
	}
	if err := runner.Up(ctx); err != nil {
		panic(err)
	}
	runner.Close()

	db, err := database.NewPool(ctx, dsn)
	if err != nil {
		panic(err)
	}
	pool = repositories.New(db)

	// Reset all data so each run is deterministic.
	if err := cleanTables(ctx, db); err != nil {
		panic(err)
	}

	code := m.Run()
	db.Close()
	os.Exit(code)
}

// cleanTables truncates all application tables for a clean e2e run.
func cleanTables(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `
		TRUNCATE TABLE audit_logs, console_tokens, webhook_events, payments, invoices,
			subscriptions, billing_customers, vm_operations, ip_allocations, vms, images,
			ip_pools, networks, plan_versions, plans, storage_pools, nodes, clusters,
			sessions, login_attempts, user_profiles, user_roles, users
		RESTART IDENTITY CASCADE`)
	return err
}

// e2eTest wires the control-plane services and a real-libvirt agent together.
type e2eTest struct {
	userSvc     *users.Service
	vmSvc       *vms.Service
	clusterSvc  *cluster.Service
	planSvc     *plans.Service
	netSvc      *networks.Service
	billingSvc  *billing.Service
	consoleSvc  *console.Service
	agentServer *grpcserver.Server
	real        *realLibvirtEnv
	factory     *agents.Factory
	agentAddr   string
	provisioner *vms.Provisioner
}

func setupE2E(t *testing.T) *e2eTest {
	t.Helper()
	ctx := context.Background()
	auditSvc := audit.NewService(pool.Audit)

	userSvc := users.NewService(pool.Users, pool.UserProfiles, pool.Sessions, pool.LoginAttempts, auditSvc, users.Options{
		SessionTTL: time.Hour, SessionTokenLen: 32, Argon2: auth.DefaultParams,
		LoginMaxAttempts: 5, LoginLockWindow: 15 * time.Minute,
	})

	clusterSvc := cluster.NewService(pool.Clusters, pool.Nodes, pool.StoragePools, auditSvc, 30*time.Second, 60*time.Second)
	planSvc := plans.NewService(pool.Plans, auditSvc)
	netSvc := networks.NewService(pool.Networks, pool.IPPools, auditSvc)

	// REAL libvirt agent: storage pool + bridge + kernel/initramfs image.
	real := newRealLibvirtEnv(t)

	factory := agents.NewFactory(grpcclient.Options{Timeout: 5 * time.Second})

	catalog := vms.NewCatalog(pool.VMs, pool.Nodes, pool.Images, netSvc, pool.Networks, pool.IPPools)
	provisioner := vms.NewProvisioner(pool.Operations, pool.VMs, catalog, agents.AgentFactoryFunc(factory), 10*time.Second, log)
	provisioner.Start(2)

	sched := scheduler.New(pool.Nodes, pool.VMs)
	macGen := macalloc.NewGenerator(pool.VMs)
	billingSvc := billing.NewService("", "", pool.Billing, auditSvc, log)

	consoleSvc := console.NewService(pool.ConsoleTokens, &e2eConsoleAgent{factory: factory}, time.Minute, auditSvc, "http://localhost:8080")

	vmSvc := vms.NewService(pool.VMs, pool.Operations, pool.Nodes, pool.Plans, netSvc, sched, macGen, auditSvc, provisioner, func(ctx context.Context, userID string) (bool, error) {
		return true, nil
	})

	// Create a cluster and register the real agent as a node.
	cluster, err := clusterSvc.CreateCluster(ctx, "e2e-cluster-"+suffix(), "e2e")
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	node, err := clusterSvc.RegisterNode(ctx, cluster.ID, "agent-"+suffix(), real.agentAddr)
	if err != nil {
		t.Fatalf("register node: %v", err)
	}
	// Mark the node healthy so the scheduler picks it.
	if err := pool.Nodes.UpdateHeartbeat(ctx, node.ID, 16, 32768, 200, 10, 10); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	return &e2eTest{
		userSvc: userSvc, vmSvc: vmSvc, clusterSvc: clusterSvc, planSvc: planSvc,
		netSvc: netSvc, billingSvc: billingSvc, consoleSvc: consoleSvc,
		agentServer: real.agentServer, real: real, factory: factory,
		agentAddr: real.agentAddr, provisioner: provisioner,
	}
}

// e2eConsoleAgent adapts the agent factory to the console service.
type e2eConsoleAgent struct {
	factory *agents.Factory
}

func (a *e2eConsoleAgent) OpenConsole(ctx context.Context, endpoint, vmID, vmName, consoleType string) (console.Stream, error) {
	client, err := a.factory.Client(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	stream, err := client.Console(ctx, vmID, vmName, consoleType)
	if err != nil {
		return nil, err
	}
	return &e2eGRPCStream{stream: stream}, nil
}

// e2eGRPCStream adapts the raw gRPC bidi stream to console.Stream.
type e2eGRPCStream struct {
	stream agentv1.AgentService_ConsoleClient
}

func (s *e2eGRPCStream) Send(data []byte) error {
	return s.stream.Send(&agentv1.ConsoleRequest{Data: data})
}

func (s *e2eGRPCStream) Recv() ([]byte, error) {
	resp, err := s.stream.Recv()
	if err != nil {
		return nil, err
	}
	return resp.GetData(), nil
}

func (s *e2eGRPCStream) Close() error {
	return s.stream.CloseSend()
}

// readRealSerialConsole connects a WebSocket client to the console gateway and
// talks to the REAL VM's serial console: reads the boot banner, sends
// "hello-real", and reads the guest's echo. The one-time token is consumed by
// the gateway.
func readRealSerialConsole(t *testing.T, te *e2eTest, rawToken, vmName string) (banner, echo string, err error) {
	t.Helper()
	gateway := consolegateway.New(te.consoleSvc, log)
	srv := httptest.NewServer(http.HandlerFunc(gateway.HandleWS))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):] + "/console/ws?token=" + rawToken
	ws, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPClient: &http.Client{Timeout: 10 * time.Second}})
	if err != nil {
		return "", "", fmt.Errorf("websocket dial: %w", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "done")

	// Read until the guest banner appears.
	var bannerBuf bytes.Buffer
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		_, data, rerr := ws.Read(context.Background())
		if rerr != nil {
			return bannerBuf.String(), "", fmt.Errorf("read banner: %w", rerr)
		}
		bannerBuf.Write(data)
		if strings.Contains(bannerBuf.String(), "console ready") {
			break
		}
	}
	if !strings.Contains(bannerBuf.String(), "console ready") {
		return bannerBuf.String(), "", fmt.Errorf("no banner within deadline: %q", bannerBuf.String())
	}

	// Send input; read the guest echo.
	if err := ws.Write(context.Background(), websocket.MessageText, []byte("hello-real\n")); err != nil {
		return bannerBuf.String(), "", fmt.Errorf("write: %w", err)
	}
	var echoBuf bytes.Buffer
	deadline = time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		_, data, rerr := ws.Read(context.Background())
		if rerr != nil {
			return bannerBuf.String(), echoBuf.String(), fmt.Errorf("read echo: %w", rerr)
		}
		echoBuf.Write(data)
		if strings.Contains(echoBuf.String(), "console-echo:hello-real") {
			break
		}
	}
	return bannerBuf.String(), echoBuf.String(), nil
}

func TestEndToEndProvisioningFlow(t *testing.T) {
	ctx := context.Background()
	te := setupE2E(t)

	// 1. User registration.
	email := fmt.Sprintf("e2e-%d@example.com", time.Now().UnixNano())
	user, err := te.userSvc.Register(ctx, users.RegistrationRequest{
		Email: email, Password: "password123",
		FirstName: "E2E", LastName: "Tester", LegalName: "E2E Tester",
		Country: "JP", PostalCode: "100-0001", State: "Tokyo", City: "Chiyoda", AddressLine1: "1-1",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// 2. Login + session authentication.
	login, err := te.userSvc.Login(ctx, email, "password123", "127.0.0.1", "e2e")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, _, err := te.userSvc.Authenticate(ctx, login.Token); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	uniq := suffix()
	plan, err := te.planSvc.Create(ctx, &models.Plan{
		Name: "e2e-plan-" + uniq, VCPU: 2, MemoryMB: 2048, DiskGB: 20, MonthlyPriceCents: 1000, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	net, err := te.netSvc.CreateNetwork(ctx, &models.Network{
		Name: "e2e-net-" + uniq, Bridge: te.real.bridgeName, DNS1: "1.1.1.1", DNS2: "1.0.0.1",
	})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	if _, err := te.netSvc.AddPool(ctx, &models.IPPool{NetworkID: net.ID, CIDR: "203.0.113.0/28", Type: "ipv4", Gateway: "203.0.113.1"}); err != nil {
		t.Fatalf("add pool: %v", err)
	}
	// Kernel-boot image: the VM boots the host kernel + a minimal initramfs
	// that runs a serial shell (real boot, no OS image needed).
	kernelURL, initrdURL, cmdline := te.real.kernelImage("ubuntu", uniq)
	img, err := pool.Images.Create(ctx, &models.Image{
		Name: "ubuntu", Version: "24.04-" + uniq, Format: "raw", SourceURL: "/dev/null",
		CloudInitCompatible: false, KernelURL: kernelURL, InitrdURL: initrdURL, Cmdline: cmdline,
	})
	if err != nil {
		t.Fatalf("create image: %v", err)
	}

	// 4. Create a VM -> async operation.
	vm, op, err := te.vmSvc.CreateVM(ctx, user.ID, vms.CreateRequest{
		PlanID: plan.ID, NetworkID: net.ID, ImageID: img.ID,
		Name: "e2e-vm", Hostname: "e2e-vm",
	}, "e2e-key-"+suffix())
	if err != nil {
		t.Fatalf("create vm: %v", err)
	}

	// 5. Wait for the operation to succeed (agent in fake mode always succeeds).
	done := make(chan struct{})
	go func() {
		opCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for {
			cur, err := pool.Operations.GetByID(opCtx, op.ID)
			if err == nil && cur.Status != models.OperationPending && cur.Status != models.OperationRunning {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("operation timed out")
	}

	final, err := pool.Operations.GetByID(ctx, op.ID)
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if final.Status != models.OperationSucceeded {
		t.Fatalf("expected operation succeeded, got %s: %s", final.Status, final.Error)
	}

	// 6. Confirm VM status is running.
	vmFinal, err := pool.VMs.GetByID(ctx, vm.ID)
	if err != nil {
		t.Fatalf("get vm: %v", err)
	}
	if vmFinal.Status != models.VMStatusRunning {
		t.Errorf("expected vm running, got %s", vmFinal.Status)
	}

	// 7. Confirm IP allocation exists.
	allocs, err := pool.IPPools.GetByVM(ctx, vm.ID)
	if err != nil {
		t.Fatalf("get allocations: %v", err)
	}
	if len(allocs) != 1 {
		t.Fatalf("expected 1 ip allocation, got %d", len(allocs))
	}
	if allocs[0].IPAddress == "" || allocs[0].IPAddress == "203.0.113.0" || allocs[0].IPAddress == "203.0.113.1" {
		t.Errorf("invalid allocated ip: %s", allocs[0].IPAddress)
	}

	// 8. The real agent defined and started the domain (kernel boot).
	vmName := "vps-" + vm.InstanceID
	state, err := te.real.adapter.GetDomainState(vmName)
	if err != nil {
		t.Fatalf("get real domain state: %v", err)
	}
	if state != agentlibvirt.StateRunning {
		t.Errorf("expected real domain running, got %s", state)
	}

	// 8b. The cloud-init seed volume was created and uploaded on the real pool.
	ciVol := "vps-" + vm.ID + "-cloudinit"
	vi, err := te.real.adapter.GetVolume(te.real.poolName, ciVol)
	if err != nil {
		t.Errorf("cloud-init volume not found on real libvirt: %v", err)
	} else if vi.CapacityBytes == 0 {
		t.Errorf("cloud-init volume empty: %+v", vi)
	}

	// 8c. Serial console over the REAL VM via the streaming Console RPC.
	serialTok, serialRaw, err := te.consoleSvc.Issue(ctx, user.ID, vm.ID, vms.DomainName(vm), te.agentAddr, console.TypeSerial)
	if err != nil {
		t.Fatalf("issue serial console: %v", err)
	}
	if serialTok.ConsoleType != console.TypeSerial || serialTok.NodeEndpoint == "" {
		t.Errorf("serial token wrong: %+v", serialTok)
	}
	// Connect through the WebSocket gateway to the real VM's serial console.
	banner, echo, err := readRealSerialConsole(t, te, serialRaw, vm.Name)
	if err != nil {
		t.Fatalf("serial console over real VM: %v", err)
	}
	if !strings.Contains(banner, "VPS real-libvirt console ready") {
		t.Errorf("unexpected real serial banner: %q", banner)
	}
	if !strings.Contains(echo, "console-echo:hello-real") {
		t.Errorf("unexpected real serial echo: %q", echo)
	}
	consumedSerial, err := te.consoleSvc.Consume(ctx, serialRaw)
	if err == nil {
		t.Error("serial token must be single-use")
	} else if consumedSerial != nil {
		t.Errorf("unexpected consumed token: %+v", consumedSerial)
	}

	// 8d. VNC token issues (OpenGraphicsFD verified in tests/real).
	vncTok, vncRaw, err := te.consoleSvc.Issue(ctx, user.ID, vm.ID, vms.DomainName(vm), te.agentAddr, console.TypeVNC)
	if err != nil {
		t.Fatalf("issue vnc console: %v", err)
	}
	if vncTok.ConsoleType != console.TypeVNC || vncTok.NodeEndpoint != te.agentAddr {
		t.Errorf("vnc token wrong: %+v", vncTok)
	}
	if _, err := te.consoleSvc.Consume(ctx, vncRaw); err != nil {
		t.Fatalf("consume vnc token: %v", err)
	}

	// 9. Stop the VM.
	stopOp, err := te.vmSvc.NewOperation(ctx, user.ID, vm.ID, models.OperationStop, "")
	if err != nil {
		t.Fatalf("stop vm: %v", err)
	}
	if _, err := te.vmSvc.WaitForOperation(ctx, stopOp.ID); err != nil {
		t.Fatalf("wait stop: %v", err)
	}
	stopped, _ := pool.VMs.GetByID(ctx, vm.ID)
	if stopped.Status != models.VMStatusStopped {
		t.Errorf("expected vm stopped, got %s", stopped.Status)
	}

	// 10. Start it again.
	startOp, err := te.vmSvc.NewOperation(ctx, user.ID, vm.ID, models.OperationStart, "")
	if err != nil {
		t.Fatalf("start vm: %v", err)
	}
	if _, err := te.vmSvc.WaitForOperation(ctx, startOp.ID); err != nil {
		t.Fatalf("wait start: %v", err)
	}

	// 11. Delete the VM.
	delOp, err := te.vmSvc.NewOperation(ctx, user.ID, vm.ID, models.OperationDelete, "")
	if err != nil {
		t.Fatalf("delete vm: %v", err)
	}
	if _, err := te.vmSvc.WaitForOperation(ctx, delOp.ID); err != nil {
		t.Fatalf("wait delete: %v", err)
	}

	// 12. Confirm IP is released and VM is terminated.
	allocsAfter, _ := pool.IPPools.GetByVM(ctx, vm.ID)
	for _, a := range allocsAfter {
		if a.Status != models.IPAllocationReleased {
			t.Errorf("ip %s not released: %s", a.IPAddress, a.Status)
		}
	}
	deleted, _ := pool.VMs.GetByID(ctx, vm.ID)
	if deleted.Status != models.VMStatusTerminated {
		t.Errorf("expected vm terminated, got %s", deleted.Status)
	}

	// 13. The real agent's domain was undefined after deletion.
	if _, err := te.real.adapter.GetDomainState(vmName); err == nil {
		t.Error("agent domain should have been removed from real libvirt")
	}

	// 14. Idempotency: creating with the same key returns the same op and does
	// not provision a second VM.
	_, op2, err := te.vmSvc.CreateVM(ctx, user.ID, vms.CreateRequest{
		PlanID: plan.ID, NetworkID: net.ID, ImageID: img.ID, Name: "dup", Hostname: "dup",
	}, op.IdempotencyKey)
	if err != nil {
		t.Fatalf("idempotent create: %v", err)
	}
	if op2.ID != op.ID {
		t.Errorf("idempotency key did not dedupe: %s != %s", op2.ID, op.ID)
	}
}

func suffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%100000)
}

// waitForServer blocks until the TCP address accepts connections.
func waitForServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server %s did not start accepting connections", addr)
}

// isISOFile checks the ISO9660 magic bytes at the start of a file.
func isISOFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 32769)
	n, err := f.Read(buf)
	if err != nil && n < 32769 {
		return false
	}
	// ISO9660 sector 16 (offset 32768) starts with "CD001".
	if n < 32769 {
		return false
	}
	return string(buf[32768:32773]) == "CD001"
}
