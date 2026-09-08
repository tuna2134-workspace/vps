// Package grpcserver implements the AgentService that the Control Plane uses
// to manage VMs on this node.
package grpcserver

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tuna2134/vps/internal/agent/libvirt"
	"github.com/tuna2134/vps/internal/agent/manager"
	"github.com/tuna2134/vps/internal/agent/metrics"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
	commonv1 "github.com/tuna2134/vps/proto/gen/common/v1"
)

// Server implements agentv1.AgentServiceServer.
type Server struct {
	agentv1.UnimplementedAgentServiceServer
	manager *manager.Manager
	libvirt libvirt.Manager
	metrics *metrics.Provider
	log     *slog.Logger
}

// New builds the agent gRPC server.
func New(mgr *manager.Manager, lv libvirt.Manager, metricsProvider *metrics.Provider, log *slog.Logger) *Server {
	s := &Server{
		manager: mgr,
		libvirt: lv,
		metrics: metricsProvider,
		log:     log,
	}
	if mgr != nil {
		s.metrics.SetStoragePool(mgr.DefaultPool())
	}
	return s
}

func okResponse() *agentv1.OperationResponse {
	return &agentv1.OperationResponse{State: commonv1.OperationState_OPERATION_STATE_SUCCEEDED}
}

func errResponse(err error) *agentv1.OperationResponse {
	return &agentv1.OperationResponse{
		State: commonv1.OperationState_OPERATION_STATE_FAILED,
		Error: &commonv1.Error{Code: "AGENT_ERROR", Message: err.Error()},
	}
}

func (s *Server) CreateVM(ctx context.Context, req *agentv1.CreateVMRequest) (*agentv1.OperationResponse, error) {
	if err := s.manager.CreateVM(ctx, req); err != nil {
		s.log.Error("create vm failed", "vm", req.GetVmId(), "error", err)
		return errResponse(err), nil
	}
	return okResponse(), nil
}

func (s *Server) DeleteVM(ctx context.Context, req *agentv1.DeleteVMRequest) (*agentv1.OperationResponse, error) {
	if err := s.manager.DeleteVM(ctx, req); err != nil {
		s.log.Error("delete vm failed", "vm", req.GetVmId(), "error", err)
		return errResponse(err), nil
	}
	return okResponse(), nil
}

func (s *Server) StartVM(ctx context.Context, req *agentv1.StartVMRequest) (*agentv1.OperationResponse, error) {
	if err := s.libvirt.StartDomain(vmName(req)); err != nil {
		return errResponse(err), nil
	}
	return okResponse(), nil
}

func (s *Server) StopVM(ctx context.Context, req *agentv1.StopVMRequest) (*agentv1.OperationResponse, error) {
	if err := s.libvirt.ShutdownDomain(vmName(req)); err != nil {
		return errResponse(err), nil
	}
	return okResponse(), nil
}

func (s *Server) ForceStopVM(ctx context.Context, req *agentv1.ForceStopVMRequest) (*agentv1.OperationResponse, error) {
	if err := s.libvirt.DestroyDomain(vmName(req)); err != nil {
		return errResponse(err), nil
	}
	return okResponse(), nil
}

func (s *Server) RebootVM(ctx context.Context, req *agentv1.RebootVMRequest) (*agentv1.OperationResponse, error) {
	if err := s.libvirt.RebootDomain(vmName(req)); err != nil {
		return errResponse(err), nil
	}
	return okResponse(), nil
}

func (s *Server) GetVM(ctx context.Context, req *agentv1.GetVMRequest) (*agentv1.VMResponse, error) {
	vmStatus, err := s.manager.GetVM(ctx, req)
	if err != nil {
		return nil, status.Error(codes.NotFound, "vm not found: "+err.Error())
	}
	return &agentv1.VMResponse{Status: vmStatus}, nil
}

func (s *Server) GetNodeStatus(ctx context.Context, req *agentv1.GetNodeStatusRequest) (*agentv1.NodeStatusResponse, error) {
	snap, err := s.snapshot()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &agentv1.NodeStatusResponse{Status: snapshotToProto(snap)}, nil
}

func (s *Server) Heartbeat(ctx context.Context, req *agentv1.HeartbeatRequest) (*agentv1.HeartbeatResponse, error) {
	snap, err := s.snapshot()
	if err != nil {
		return &agentv1.HeartbeatResponse{Ok: false}, nil
	}
	return &agentv1.HeartbeatResponse{Ok: snap.Healthy}, nil
}

// Console is a bidirectional stream to a VM's console (serial or VNC), opened
// through the official virDomainOpenConsole / virDomainOpenGraphicsFD APIs.
// The first frame carries the VM identity and console type; subsequent frames
// are input bytes. Console output is streamed back.
func (s *Server) Console(stream agentv1.AgentService_ConsoleServer) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Error(codes.InvalidArgument, "console stream must start with a vm id: "+err.Error())
	}
	vmName := first.GetVmName()
	if vmName == "" {
		vmName = "vps-" + first.GetVmId()
	}

	var cs libvirt.ConsoleStream
	switch first.GetConsoleType() {
	case "vnc":
		cs, err = s.libvirt.OpenGraphics(vmName)
		if err != nil {
			return status.Error(codes.NotFound, "vnc console unavailable: "+err.Error())
		}
	default: // serial
		cs, err = s.libvirt.OpenConsole(vmName)
		if err != nil {
			return status.Error(codes.NotFound, "serial console unavailable: "+err.Error())
		}
	}
	defer cs.Close()

	ctx := stream.Context()

	// Console -> client
	recvErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := cs.Recv(buf)
			if n > 0 {
				if sendErr := stream.Send(&agentv1.ConsoleResponse{Data: buf[:n]}); sendErr != nil {
					recvErr <- sendErr
					return
				}
			}
			if err != nil {
				recvErr <- err
				return
			}
		}
	}()

	// Client -> console
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		default:
		}
		req, err := stream.Recv()
		if err != nil {
			return nil // client closed
		}
		if len(req.GetData()) == 0 {
			continue
		}
		if _, err := cs.Send(req.GetData()); err != nil {
			return err
		}
	}
}

func (s *Server) snapshot() (*metrics.Snapshot, error) {
	return s.metrics.Sample()
}

func snapshotToProto(snap *metrics.Snapshot) *commonv1.NodeStatus {
	return &commonv1.NodeStatus{
		Hostname:          snap.Hostname,
		CpuCount:          uint32(snap.CPUCount),
		MemoryTotalBytes:  uint64(snap.MemoryTotalBytes),
		MemoryFreeBytes:   uint64(snap.MemoryFreeBytes),
		StorageTotalBytes: uint64(snap.StorageTotalBytes),
		StorageFreeBytes:  uint64(snap.StorageFreeBytes),
		CpuUsagePercent:   float32(snap.CPUUsagePercent),
		Healthy:           snap.Healthy,
	}
}

// vmName resolves the domain name from a request (mirrors control plane).
func vmName[T interface {
	GetVmId() string
	GetVmName() string
}](req T) string {
	if n := req.GetVmName(); n != "" {
		return n
	}
	return "vps-" + req.GetVmId()
}

var _ = errors.New
