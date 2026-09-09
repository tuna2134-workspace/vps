// Package grpcserver implements the AgentService that the Control Plane uses
// to manage VMs on this node.
package grpcserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/tuna2134/vps/internal/agent/libvirt"
	"github.com/tuna2134/vps/internal/agent/manager"
	"github.com/tuna2134/vps/internal/agent/metrics"
	"github.com/tuna2134/vps/internal/agent/operations"

	agentv1 "github.com/tuna2134/vps/proto/gen/agent/v1"
	commonv1 "github.com/tuna2134/vps/proto/gen/common/v1"
)

// Server implements agentv1.AgentServiceServer.
type Server struct {
	agentv1.UnimplementedAgentServiceServer
	manager    *manager.Manager
	libvirt    libvirt.Manager
	metrics    *metrics.Provider
	operations *operations.Service
	log        *slog.Logger
}

// New builds the agent gRPC server. opSvc provides agent-side operation
// idempotency; it may be nil to disable deduplication.
func New(mgr *manager.Manager, lv libvirt.Manager, metricsProvider *metrics.Provider, opSvc *operations.Service, log *slog.Logger) *Server {
	s := &Server{
		manager:    mgr,
		libvirt:    lv,
		metrics:    metricsProvider,
		operations: opSvc,
		log:        log,
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

// execute runs a mutating operation exactly once per operation_id.
func (s *Server) execute(ctx context.Context, opID, opType, vmID string, req proto.Message, fn func(context.Context) error) (*agentv1.OperationResponse, error) {
	if s.operations == nil {
		if err := fn(ctx); err != nil {
			return errResponse(err), nil
		}
		return okResponse(), nil
	}
	result, err := s.operations.Execute(ctx, opID, opType, vmID, hashRequest(req), fn)
	if err != nil {
		s.log.Error("operation dedup failed", "operation", opID, "type", opType, "error", err)
		return errResponse(err), nil
	}
	if result.Succeeded() {
		return okResponse(), nil
	}
	return errResponse(result.Error), nil
}

func (s *Server) CreateVM(ctx context.Context, req *agentv1.CreateVMRequest) (*agentv1.OperationResponse, error) {
	resp, rpcErr := s.execute(ctx, req.GetOperationId(), "create", req.GetVmId(), req,
		func(ctx context.Context) error { return s.manager.CreateVM(ctx, req) })
	if rpcErr != nil {
		s.log.Error("create vm dedup failed", "vm", req.GetVmId(), "error", rpcErr)
	}
	return resp, nil
}

func (s *Server) DeleteVM(ctx context.Context, req *agentv1.DeleteVMRequest) (*agentv1.OperationResponse, error) {
	resp, rpcErr := s.execute(ctx, req.GetOperationId(), "delete", req.GetVmId(), req,
		func(ctx context.Context) error { return s.manager.DeleteVM(ctx, req) })
	if rpcErr != nil {
		s.log.Error("delete vm dedup failed", "vm", req.GetVmId(), "error", rpcErr)
	}
	return resp, nil
}

func (s *Server) StartVM(ctx context.Context, req *agentv1.StartVMRequest) (*agentv1.OperationResponse, error) {
	return s.execute(ctx, req.GetOperationId(), "start", req.GetVmId(), req,
		func(ctx context.Context) error { return s.libvirt.StartDomain(vmName(req)) })
}

func (s *Server) StopVM(ctx context.Context, req *agentv1.StopVMRequest) (*agentv1.OperationResponse, error) {
	return s.execute(ctx, req.GetOperationId(), "stop", req.GetVmId(), req,
		func(ctx context.Context) error { return s.libvirt.ShutdownDomain(vmName(req)) })
}

func (s *Server) ForceStopVM(ctx context.Context, req *agentv1.ForceStopVMRequest) (*agentv1.OperationResponse, error) {
	return s.execute(ctx, req.GetOperationId(), "force_stop", req.GetVmId(), req,
		func(ctx context.Context) error { return s.libvirt.DestroyDomain(vmName(req)) })
}

func (s *Server) RebootVM(ctx context.Context, req *agentv1.RebootVMRequest) (*agentv1.OperationResponse, error) {
	return s.execute(ctx, req.GetOperationId(), "reboot", req.GetVmId(), req,
		func(ctx context.Context) error { return s.libvirt.RebootDomain(vmName(req)) })
}

// hashRequest returns a stable SHA-256 digest of the request. The proto
// messages used here contain only scalars and ordered repeated fields, so
// proto.Marshal is deterministic for them.
func hashRequest(req proto.Message) string {
	b, err := proto.Marshal(req)
	if err != nil {
		return "unhashable"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
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

// ListVMs reports every domain on the node with its actual runtime state. The
// Control Plane reconciles its DB against this.
func (s *Server) ListVMs(ctx context.Context, req *agentv1.ListVMsRequest) (*agentv1.ListVMsResponse, error) {
	names, err := s.libvirt.ListDomains()
	if err != nil {
		return nil, status.Error(codes.Internal, "list domains: "+err.Error())
	}
	resp := &agentv1.ListVMsResponse{}
	for _, name := range names {
		st, err := s.libvirt.GetDomainState(name)
		if err != nil {
			s.log.Warn("list vm state failed", "vm", name, "error", err)
			continue
		}
		resp.Vms = append(resp.Vms, &agentv1.VMSummary{
			VmName: name,
			State:  stateToProto(st),
		})
	}
	return resp, nil
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

// stateToProto maps the normalized agent VM state onto the common proto enum.
func stateToProto(st libvirt.VMState) commonv1.VMState {
	switch st {
	case libvirt.StateRunning:
		return commonv1.VMState_VM_STATE_RUNNING
	case libvirt.StateBlocked:
		return commonv1.VMState_VM_STATE_BLOCKED
	case libvirt.StatePaused:
		return commonv1.VMState_VM_STATE_PAUSED
	case libvirt.StateShutdown:
		return commonv1.VMState_VM_STATE_SHUTDOWN
	case libvirt.StateCrashed:
		return commonv1.VMState_VM_STATE_CRASHED
	case libvirt.StatePMSuspended:
		return commonv1.VMState_VM_STATE_PMSUSPENDED
	case libvirt.StateShutoff:
		return commonv1.VMState_VM_STATE_SHUTOFF
	default:
		return commonv1.VMState_VM_STATE_NOSTATE
	}
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
