package api

import (
	"net/http"
	"strings"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/vms"
)

type createVMRequest struct {
	PlanID       string   `json:"plan_id"`
	NetworkID    string   `json:"network_id"`
	ImageID      string   `json:"image_id"`
	Name         string   `json:"name"`
	Hostname     string   `json:"hostname"`
	SSHKeys      []string `json:"ssh_keys"`
	RootPassword string   `json:"root_password,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type createVMResponse struct {
	OperationID string `json:"operation_id"`
	VMID        string `json:"vm_id"`
}

type operationResponse struct {
	ID            string `json:"id"`
	VMID          string `json:"vm_id"`
	OperationType string `json:"operation_type"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// VMHandlers exposes VM and operation endpoints.
type VMHandlers struct {
	vms *vms.Service
}

func NewVMHandlers(vmSvc *vms.Service) *VMHandlers {
	return &VMHandlers{vms: vmSvc}
}

func (h *VMHandlers) CreateVM(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	var req createVMRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.PlanID == "" || req.NetworkID == "" || req.ImageID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "plan_id, network_id and image_id are required")
		return
	}
	if req.Name == "" {
		req.Name = req.Hostname
	}
	if req.Hostname == "" {
		req.Hostname = req.Name
	}

	vm, op, err := h.vms.CreateVM(r.Context(), u.ID, vms.CreateRequest{
		PlanID:       req.PlanID,
		NetworkID:    req.NetworkID,
		ImageID:      req.ImageID,
		Name:         req.Name,
		Hostname:     req.Hostname,
		SSHKeys:      req.SSHKeys,
		RootPassword: req.RootPassword,
	}, req.IdempotencyKey)
	if err != nil {
		writeVMError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, createVMResponse{OperationID: op.ID, VMID: vm.ID})
}

func (h *VMHandlers) ListVMs(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	vms, err := h.vms.ListVMs(r.Context(), u.ID)
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, vms)
}

func (h *VMHandlers) GetVM(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	vmID := pathSegment(r, "/v1/vms/")
	vm, err := h.vms.GetVM(r.Context(), u.ID, vmID)
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, vm)
}

func (h *VMHandlers) VMOperation(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	rest := strings.TrimPrefix(r.URL.Path, "/v1/vms/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid path")
		return
	}
	vmID := parts[0]
	action := parts[1]
	key := r.Header.Get("Idempotency-Key")

	var opType models.OperationType
	switch action {
	case "start":
		opType = models.OperationStart
	case "stop":
		opType = models.OperationStop
	case "force_stop":
		opType = models.OperationForceStop
	case "reboot":
		opType = models.OperationReboot
	default:
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "unknown action")
		return
	}

	op, err := h.vms.NewOperation(r.Context(), u.ID, vmID, opType, key)
	if err != nil {
		writeVMError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, opResponse(op))
}

func (h *VMHandlers) DeleteVM(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	vmID := pathSegment(r, "/v1/vms/")
	op, err := h.vms.NewOperation(r.Context(), u.ID, vmID, models.OperationDelete, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeVMError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, opResponse(op))
}

func (h *VMHandlers) GetOperation(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	opID := pathSegment(r, "/v1/operations/")
	op, err := h.vms.GetOperation(r.Context(), u.ID, opID)
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, opResponse(op))
}

func opResponse(op *models.VMOperation) operationResponse {
	return operationResponse{
		ID:            op.ID,
		VMID:          op.VMID,
		OperationType: string(op.OperationType),
		Status:        string(op.Status),
		Error:         op.Error,
		CreatedAt:     op.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:     op.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// writeVMError maps VM service errors to HTTP responses.
func writeVMError(w http.ResponseWriter, err error) {
	switch {
	case err == vms.ErrNoCapacity:
		writeError(w, http.StatusServiceUnavailable, "NO_CAPACITY", "no node has sufficient capacity")
	case err == vms.ErrInvalidState:
		writeError(w, http.StatusConflict, "INVALID_STATE", "the vm is not in a valid state for this operation")
	case err == vms.ErrBillingRequired:
		writeError(w, http.StatusPaymentRequired, "BILLING_REQUIRED", "an active subscription is required")
	default:
		mapError(w, err)
	}
}

// pathSegment returns the trailing segment after the given prefix.
func pathSegment(r *http.Request, prefix string) string {
	return strings.TrimPrefix(r.URL.Path, prefix)
}