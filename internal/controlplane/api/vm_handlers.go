package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/vms"
)

type createVMRequest struct {
	PlanID         string   `json:"plan_id"`
	NetworkID      string   `json:"network_id"`
	ImageID        string   `json:"image_id"`
	Name           string   `json:"name"`
	Hostname       string   `json:"hostname"`
	SSHKeys        []string `json:"ssh_keys"`
	RootPassword   string   `json:"root_password,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
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

func (h *VMHandlers) CreateVM(c *gin.Context) {
	u := UserFrom(c)
	var req createVMRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.PlanID == "" || req.NetworkID == "" || req.ImageID == "" {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "plan_id, network_id and image_id are required")
		return
	}
	if req.Name == "" {
		req.Name = req.Hostname
	}
	if req.Hostname == "" {
		req.Hostname = req.Name
	}

	vm, op, err := h.vms.CreateVM(c.Request.Context(), u.ID, vms.CreateRequest{
		PlanID:       req.PlanID,
		NetworkID:    req.NetworkID,
		ImageID:      req.ImageID,
		Name:         req.Name,
		Hostname:     req.Hostname,
		SSHKeys:      req.SSHKeys,
		RootPassword: req.RootPassword,
	}, req.IdempotencyKey)
	if err != nil {
		writeVMError(c, err)
		return
	}
	writeData(c, http.StatusAccepted, createVMResponse{OperationID: op.ID, VMID: vm.ID})
}

func (h *VMHandlers) ListVMs(c *gin.Context) {
	u := UserFrom(c)
	vms, err := h.vms.ListVMs(c.Request.Context(), u.ID)
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, vms)
}

func (h *VMHandlers) GetVM(c *gin.Context) {
	u := UserFrom(c)
	vm, err := h.vms.GetVM(c.Request.Context(), u.ID, c.Param("id"))
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, vm)
}

func (h *VMHandlers) VMOperation(c *gin.Context) {
	u := UserFrom(c)
	action := c.Param("action")
	key := c.Request.Header.Get("Idempotency-Key")

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
		writeError(c, http.StatusNotFound, "RESOURCE_NOT_FOUND", "unknown action")
		return
	}

	op, err := h.vms.NewOperation(c.Request.Context(), u.ID, c.Param("id"), opType, key)
	if err != nil {
		writeVMError(c, err)
		return
	}
	writeData(c, http.StatusAccepted, opResponse(op))
}

func (h *VMHandlers) DeleteVM(c *gin.Context) {
	u := UserFrom(c)
	op, err := h.vms.NewOperation(c.Request.Context(), u.ID, c.Param("id"), models.OperationDelete, c.Request.Header.Get("Idempotency-Key"))
	if err != nil {
		writeVMError(c, err)
		return
	}
	writeData(c, http.StatusAccepted, opResponse(op))
}

func (h *VMHandlers) GetOperation(c *gin.Context) {
	u := UserFrom(c)
	op, err := h.vms.GetOperation(c.Request.Context(), u.ID, c.Param("id"))
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, opResponse(op))
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
func writeVMError(c *gin.Context, err error) {
	switch {
	case err == vms.ErrNoCapacity:
		writeError(c, http.StatusServiceUnavailable, "NO_CAPACITY", "no node has sufficient capacity")
	case err == vms.ErrInvalidState:
		writeError(c, http.StatusConflict, "INVALID_STATE", "the vm is not in a valid state for this operation")
	case err == vms.ErrBillingRequired:
		writeError(c, http.StatusPaymentRequired, "BILLING_REQUIRED", "an active subscription is required")
	default:
		mapError(c, err)
	}
}
