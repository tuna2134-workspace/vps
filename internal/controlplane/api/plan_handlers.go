package api

import (
	"net/http"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/plans"
)

type planRequest struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	VCPU               int    `json:"vcpu"`
	MemoryMB           int    `json:"memory_mb"`
	DiskGB             int    `json:"disk_gb"`
	BandwidthGB        int    `json:"bandwidth_gb"`
	NetworkSpeedMbps   int    `json:"network_speed_mbps"`
	IPv4Count          int    `json:"ipv4_count"`
	IPv6Prefix         int    `json:"ipv6_prefix"`
	MonthlyPriceCents  int    `json:"monthly_price_cents"`
	Currency           string `json:"currency"`
}

type PlanHandlers struct {
	plans *plans.Service
}

func NewPlanHandlers(planSvc *plans.Service) *PlanHandlers {
	return &PlanHandlers{plans: planSvc}
}

func (h *PlanHandlers) List(w http.ResponseWriter, r *http.Request) {
	p, err := h.plans.List(r.Context(), r.URL.Query().Get("active") != "false")
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, p)
}

func (h *PlanHandlers) Get(w http.ResponseWriter, r *http.Request) {
	p, err := h.plans.Get(r.Context(), pathSegment(r, "/v1/plans/"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, p)
}

func (h *PlanHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var req planRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Name == "" || req.VCPU <= 0 || req.MemoryMB <= 0 || req.DiskGB <= 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name, vcpu, memory_mb and disk_gb are required")
		return
	}
	created, err := h.plans.Create(r.Context(), &models.Plan{
		Name:              req.Name,
		Description:       req.Description,
		VCPU:              req.VCPU,
		MemoryMB:          req.MemoryMB,
		DiskGB:            req.DiskGB,
		BandwidthGB:       req.BandwidthGB,
		NetworkSpeedMbps:  req.NetworkSpeedMbps,
		IPv4Count:         req.IPv4Count,
		IPv6Prefix:        req.IPv6Prefix,
		MonthlyPriceCents: req.MonthlyPriceCents,
		Currency:          req.Currency,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeData(w, http.StatusCreated, created)
}

func (h *PlanHandlers) Update(w http.ResponseWriter, r *http.Request) {
	planID := pathSegment(r, "/v1/plans/")
	var req planRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	updated, err := h.plans.Update(r.Context(), planID, &models.Plan{
		Name:              req.Name,
		Description:       req.Description,
		VCPU:              req.VCPU,
		MemoryMB:          req.MemoryMB,
		DiskGB:            req.DiskGB,
		BandwidthGB:       req.BandwidthGB,
		NetworkSpeedMbps:  req.NetworkSpeedMbps,
		IPv4Count:         req.IPv4Count,
		IPv6Prefix:        req.IPv6Prefix,
		MonthlyPriceCents: req.MonthlyPriceCents,
		Currency:          req.Currency,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, updated)
}

func (h *PlanHandlers) SetActive(w http.ResponseWriter, r *http.Request) {
	planID := pathSegment(r, "/v1/plans/")
	if err := h.plans.SetActive(r.Context(), planID, r.URL.Query().Get("active") != "false"); err != nil {
		mapServiceError(w, err)
		return
	}
	writeEmpty(w)
}

func (h *PlanHandlers) ListVersions(w http.ResponseWriter, r *http.Request) {
	planID := pathSegment(r, "/v1/plans/")
	versions, err := h.plans.ListVersions(r.Context(), planID)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, versions)
}