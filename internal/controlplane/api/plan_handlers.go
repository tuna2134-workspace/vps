package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/plans"
)

type planRequest struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	VCPU              int    `json:"vcpu"`
	MemoryMB          int    `json:"memory_mb"`
	DiskGB            int    `json:"disk_gb"`
	BandwidthGB       int    `json:"bandwidth_gb"`
	NetworkSpeedMbps  int    `json:"network_speed_mbps"`
	IPv4Count         int    `json:"ipv4_count"`
	IPv6Prefix        int    `json:"ipv6_prefix"`
	MonthlyPriceCents int    `json:"monthly_price_cents"`
	Currency          string `json:"currency"`
}

type PlanHandlers struct {
	plans *plans.Service
}

func NewPlanHandlers(planSvc *plans.Service) *PlanHandlers {
	return &PlanHandlers{plans: planSvc}
}

func (h *PlanHandlers) List(c *gin.Context) {
	p, err := h.plans.List(c.Request.Context(), c.Query("active") != "false")
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, p)
}

func (h *PlanHandlers) Get(c *gin.Context) {
	p, err := h.plans.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, p)
}

func (h *PlanHandlers) Create(c *gin.Context) {
	var req planRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Name == "" || req.VCPU <= 0 || req.MemoryMB <= 0 || req.DiskGB <= 0 {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "name, vcpu, memory_mb and disk_gb are required")
		return
	}
	created, err := h.plans.Create(c.Request.Context(), &models.Plan{
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
		mapServiceError(c, err)
		return
	}
	writeData(c, http.StatusCreated, created)
}

func (h *PlanHandlers) Update(c *gin.Context) {
	planID := c.Param("id")
	var req planRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	updated, err := h.plans.Update(c.Request.Context(), planID, &models.Plan{
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
		mapServiceError(c, err)
		return
	}
	writeData(c, http.StatusOK, updated)
}

func (h *PlanHandlers) SetActive(c *gin.Context) {
	planID := c.Param("id")
	if err := h.plans.SetActive(c.Request.Context(), planID, c.Query("active") != "false"); err != nil {
		mapServiceError(c, err)
		return
	}
	writeEmpty(c)
}

func (h *PlanHandlers) ListVersions(c *gin.Context) {
	planID := c.Param("id")
	versions, err := h.plans.ListVersions(c.Request.Context(), planID)
	if err != nil {
		mapServiceError(c, err)
		return
	}
	writeData(c, http.StatusOK, versions)
}
