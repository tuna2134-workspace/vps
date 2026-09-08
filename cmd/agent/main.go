// Command agent runs a compute node agent: it exposes the gRPC AgentService
// and manages VMs on the local host via libvirt, iptables, and cloud-init.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joho/godotenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/tuna2134/vps/internal/agent/config"
	"github.com/tuna2134/vps/internal/agent/grpcserver"
	"github.com/tuna2134/vps/internal/agent/image"
	agentlibvirt "github.com/tuna2134/vps/internal/agent/libvirt"
	"github.com/tuna2134/vps/internal/agent/manager"
	"github.com/tuna2134/vps/internal/agent/metrics"
	"github.com/tuna2134/vps/internal/agent/network"
	"github.com/tuna2134/vps/internal/agent/storage"
	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
)

func main() {
	_ = godotenv.Load()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("agent terminated", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}

	var lv agentlibvirt.Manager
	if cfg.FakeMode {
		log.Info("running in fake mode (no hypervisor)")
		lv = agentlibvirt.NewFakeManager()
	} else {
		lv = agentlibvirt.NewAdapter(cfg.LibvirtURI)
		if err := lv.Connect(); err != nil {
			return err
		}
		defer lv.Close()
	}

	store := storage.New(lv)
	fetcher := image.New(filepath.Join(cfg.WorkDir, "images"))

	var ipt *network.IptablesManager
	if cfg.FakeMode {
		ipt = nil
	} else {
		var err error
		ipt, err = network.NewIptablesManager()
		if err != nil {
			log.Warn("iptables unavailable (continuing)", "error", err)
		}
	}

	mgr := manager.New(lv, store, ipt, fetcher, cfg.WorkDir, cfg.StoragePool, log)
	metricsProvider := metrics.NewProvider(lv)
	server := grpcserver.New(mgr, lv, metricsProvider, log, cfg.FakeMode)

	opts := []grpc.ServerOption{}
	if cfg.TLS {
		creds, err := loadAgentTLS(cfg.CertFile, cfg.KeyFile, cfg.CAFile)
		if err != nil {
			return err
		}
		opts = append(opts, grpc.Creds(creds))
	}

	grpcServer := grpc.NewServer(opts...)
	agentv1.RegisterAgentServiceServer(grpcServer, server)

	lis, err := net.Listen("tcp", cfg.GRPCListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.GRPCListenAddress, err)
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("agent listening", "addr", cfg.GRPCListenAddress, "agent_id", cfg.AgentID)
		if err := grpcServer.Serve(lis); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down agent")
		grpcServer.GracefulStop()
		return nil
	case err := <-errCh:
		return err
	}
}

// loadAgentTLS builds mTLS credentials for the agent: it presents its own
// certificate and requires the client (Control Plane) to present a
// certificate signed by the same CA.
func loadAgentTLS(certFile, keyFile, caFile string) (credentials.TransportCredentials, error) {
	if certFile == "" || keyFile == "" || caFile == "" {
		return nil, errors.New("TLS enabled but cert/key/ca files not configured")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load agent cert: %w", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read ca file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	}), nil
}
