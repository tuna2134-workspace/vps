// Command control-plane runs the VPS hosting platform Control Plane:
// REST API, authentication, billing, scheduling, and cluster metadata.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/tuna2134/vps/internal/controlplane/agents"
	"github.com/tuna2134/vps/internal/controlplane/api"
	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/auth"
	"github.com/tuna2134/vps/internal/controlplane/billing"
	"github.com/tuna2134/vps/internal/controlplane/cluster"
	"github.com/tuna2134/vps/internal/controlplane/config"
	"github.com/tuna2134/vps/internal/controlplane/console"
	consolegateway "github.com/tuna2134/vps/internal/controlplane/console/gateway"
	"github.com/tuna2134/vps/internal/controlplane/database"
	"github.com/tuna2134/vps/internal/controlplane/grpcclient"
	"github.com/tuna2134/vps/internal/controlplane/images"
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

func main() {
	_ = godotenv.Load()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("control plane terminated", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Wait for PostgreSQL to be reachable, then apply migrations.
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := migrations.WaitForDB(waitCtx, cfg.DatabaseURL, log, 2*time.Second); err != nil {
		return err
	}
	runner, err := migrations.New(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return err
	}
	if err := runner.Up(ctx); err != nil {
		runner.Close()
		return err
	}
	runner.Close()
	log.Info("database migrations applied")

	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	repos := repositories.New(pool)
	auditSvc := audit.NewService(repos.Audit)

	// --- Services ---
	userSvc := users.NewService(
		repos.Users, repos.UserProfiles, repos.Sessions, repos.LoginAttempts, auditSvc,
		users.Options{
			SessionTTL:       cfg.SessionTTL,
			SessionTokenLen:  cfg.SessionTokenLen,
			Argon2:           auth.Params{Memory: cfg.Argon2Memory, Iterations: cfg.Argon2Iterations, Parallelism: cfg.Argon2Parallelism, SaltLength: cfg.Argon2SaltLength, KeyLength: cfg.Argon2KeyLength},
			LoginMaxAttempts: cfg.LoginMaxAttempts,
			LoginLockWindow:  cfg.LoginLockWindow,
		},
	)

	clusterSvc := cluster.NewService(repos.Clusters, repos.Nodes, repos.StoragePools, auditSvc, cfg.NodeDegradedAfter, cfg.NodeOfflineAfter)
	planSvc := plans.NewService(repos.Plans, auditSvc)
	imageSvc := images.NewService(repos.Images, auditSvc)
	networkSvc := networks.NewService(repos.Networks, repos.IPPools, auditSvc)

	// --- Agent connections ---
	agentFactory := agents.NewFactory(grpcclient.Options{
		TLS:        cfg.TLSEnabled,
		CertFile:   cfg.TLSCertFile,
		KeyFile:    cfg.TLSKeyFile,
		CAFile:     cfg.TLSCAFile,
		ServerName: cfg.ServerName,
		Timeout:    cfg.GRPCClientTimeout,
	})
	defer agentFactory.CloseAll()

	catalog := vms.NewCatalog(repos.VMs, repos.Nodes, repos.Images, networkSvc, repos.Networks, repos.IPPools)
	provisioner := vms.NewProvisioner(repos.Operations, repos.VMs, catalog, agents.AgentFactoryFunc(agentFactory), cfg.GRPCClientTimeout, log)

	sched := scheduler.New(repos.Nodes, repos.VMs)
	macGen := macalloc.NewGenerator(repos.VMs)

	// --- Billing ---
	billingSvc := billing.NewService(cfg.StripeSecretKey, cfg.StripeWebhookSecret, repos.Billing, auditSvc, log)
	billingGate := func(ctx context.Context, userID string) (bool, error) {
		sub, err := repos.Billing.GetSubscriptionByUser(ctx, userID)
		if err != nil {
			if errors.Is(err, repositories.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		return sub.BillingStatus == "active", nil
	}

	// --- Console ---
	consoleAgent := &agentConsoleAdapter{factory: agentFactory}
	consoleSvc := console.NewService(repos.ConsoleTokens, consoleAgent, cfg.ConsoleTokenTTL, auditSvc, cfg.PublicBaseURL)

	vmSvc := vms.NewService(repos.VMs, repos.Operations, repos.Nodes, repos.Plans, networkSvc, sched, macGen, auditSvc, provisioner, billingGate)

	// Wire billing lifecycle callbacks into VM lifecycle.
	billingSvc.OnSubscriptionCanceled = func(ctx context.Context, stripeSubID string) error {
		sub, err := repos.Billing.GetSubscriptionByStripeID(ctx, stripeSubID)
		if err != nil {
			return err
		}
		if sub.VMID != "" {
			_, err := vmSvc.NewOperation(ctx, sub.UserID, sub.VMID, models.OperationTerminate, "")
			return err
		}
		return nil
	}

	// --- HTTP API ---
	wsGateway := consolegateway.New(consoleSvc, log)
	router := api.NewRouter(api.Dependencies{
		Users:      userSvc,
		VMs:        vmSvc,
		Plans:      planSvc,
		Networks:   networkSvc,
		Clusters:   clusterSvc,
		Images:     imageSvc,
		Billing:    billingSvc,
		Console:    consoleSvc,
		ConsoleWS:  wsGateway.HandleWS,
		ReadyCheck: func(ctx context.Context) error { return pool.Ping(ctx) },
	}, log)

	server := &http.Server{
		Addr:              cfg.HTTPListenAddress,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// --- Background loops ---
	provisioner.Start(4)
	defer provisioner.Stop()

	go nodeHeartbeatPoller(ctx, clusterSvc, agentFactory, cfg, log)
	go nodeStatusReconciler(ctx, clusterSvc, log)
	go sessionJanitor(ctx, repos.Sessions, log)

	errCh := make(chan error, 1)
	go func() {
		log.Info("control plane listening", "addr", cfg.HTTPListenAddress)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancelShutdown()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func nodeStatusReconciler(ctx context.Context, svc *cluster.Service, log *slog.Logger) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := svc.ReconcileStatuses(ctx); err != nil {
				log.Warn("node status reconciliation failed", "error", err)
			}
		}
	}
}

// nodeHeartbeatPoller actively queries each registered node for its status
// and updates the node record. Agents that do not respond are flagged by the
// reconciler.
func nodeHeartbeatPoller(ctx context.Context, svc *cluster.Service, factory *agents.Factory, cfg *config.Config, log *slog.Logger) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nodes, err := svc.ListNodes(ctx, "")
			if err != nil {
				log.Warn("list nodes for heartbeat failed", "error", err)
				continue
			}
			for _, node := range nodes {
				if node.Status == models.NodeStatusDisabled {
					continue
				}
				client, err := factory.Client(ctx, node.AgentEndpoint)
				if err != nil {
					log.Warn("connect to node failed", "node", node.ID, "error", err)
					continue
				}
				resp, err := client.GetNodeStatus(ctx, &agentv1.GetNodeStatusRequest{}, 10*time.Second)
				if err != nil {
					log.Warn("node heartbeat failed", "node", node.ID, "error", err)
					continue
				}
				st := resp.GetStatus()
				metrics := &models.NodeMetrics{
					Hostname:          st.GetHostname(),
					CPUCapacity:       int(st.GetCpuCount()),
					MemoryTotalBytes:  int64(st.GetMemoryTotalBytes()),
					MemoryFreeBytes:   int64(st.GetMemoryFreeBytes()),
					StorageTotalBytes: int64(st.GetStorageTotalBytes()),
					StorageFreeBytes:  int64(st.GetStorageFreeBytes()),
					CPUUsagePercent:   float64(st.GetCpuUsagePercent()),
					Healthy:           st.GetHealthy(),
				}
				if err := svc.Heartbeat(ctx, node.Name, metrics); err != nil {
					log.Warn("record node heartbeat failed", "node", node.ID, "error", err)
				}
			}
		}
	}
}

func sessionJanitor(ctx context.Context, sessions *repositories.SessionRepository, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := sessions.DeleteExpired(ctx); err != nil {
				log.Warn("session janitor failed", "error", err)
			} else if n > 0 {
				log.Info("expired sessions cleaned", "count", n)
			}
		}
	}
}

// agentConsoleAdapter adapts the agent factory to the console's AgentClient.
type agentConsoleAdapter struct {
	factory *agents.Factory
}

func (a *agentConsoleAdapter) GetConsoleToken(ctx context.Context, endpoint, vmID, vmName, consoleType string) (*console.Endpoint, error) {
	client, err := a.factory.Client(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	resp, err := client.GetConsoleToken(ctx, &agentv1.GetConsoleTokenRequest{
		VmId:        vmID,
		VmName:      vmName,
		ConsoleType: consoleType,
	}, 15*time.Second)
	if err != nil {
		return nil, err
	}
	return &console.Endpoint{
		ConsoleType: consoleType,
		Host:        resp.GetHost(),
		Port:        int(resp.GetPort()),
		Path:        resp.GetSerialPath(),
	}, nil
}
