package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/networks"
)

type networkRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Bridge      string `json:"bridge"`
	IPv4CIDR    string `json:"ipv4_cidr"`
	IPv6CIDR    string `json:"ipv6_cidr"`
	DNS1        string `json:"dns1"`
	DNS2        string `json:"dns2"`
}

type poolRequest struct {
	CIDR                   string `json:"cidr"`
	Type                   string `json:"type"`
	Gateway                string `json:"gateway"`
	AllocationType         string `json:"allocation_type"`
	DelegationPrefixLength int    `json:"delegation_prefix_length"`
}

type NetworkHandlers struct {
	networks *networks.Service
}

func NewNetworkHandlers(netSvc *networks.Service) *NetworkHandlers {
	return &NetworkHandlers{networks: netSvc}
}

func (h *NetworkHandlers) List(c *gin.Context) {
	n, err := h.networks.ListNetworks(c.Request.Context(), c.Query("active") != "false")
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, n)
}

func (h *NetworkHandlers) Get(c *gin.Context) {
	n, err := h.networks.GetNetwork(c.Request.Context(), c.Param("id"))
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, n)
}

func (h *NetworkHandlers) Create(c *gin.Context) {
	var req networkRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Name == "" || req.Bridge == "" {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "name and bridge are required")
		return
	}
	created, err := h.networks.CreateNetwork(c.Request.Context(), &models.Network{
		Name:        req.Name,
		Description: req.Description,
		Bridge:      req.Bridge,
		IPv4CIDR:    req.IPv4CIDR,
		IPv6CIDR:    req.IPv6CIDR,
		DNS1:        req.DNS1,
		DNS2:        req.DNS2,
	})
	if err != nil {
		if err == networks.ErrConflict {
			writeError(c, http.StatusConflict, "CONFLICT", "network already exists")
			return
		}
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	writeData(c, http.StatusCreated, created)
}

func (h *NetworkHandlers) AddPool(c *gin.Context) {
	networkID := c.Param("id")
	var req poolRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.CIDR == "" || (req.Type != "ipv4" && req.Type != "ipv6") {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "cidr and type (ipv4|ipv6) are required")
		return
	}
	created, err := h.networks.AddPool(c.Request.Context(), &models.IPPool{
		NetworkID:              networkID,
		CIDR:                   req.CIDR,
		Type:                   req.Type,
		Gateway:                req.Gateway,
		AllocationType:         req.AllocationType,
		DelegationPrefixLength: req.DelegationPrefixLength,
	})
	if err != nil {
		if err == networks.ErrConflict {
			writeError(c, http.StatusConflict, "CONFLICT", "pool already exists")
			return
		}
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	writeData(c, http.StatusCreated, created)
}

func (h *NetworkHandlers) ListPools(c *gin.Context) {
	networkID := c.Param("id")
	pools, err := h.networks.ListPools(c.Request.Context(), networkID)
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, pools)
}

func (h *NetworkHandlers) SetStatus(c *gin.Context) {
	networkID := c.Param("id")
	status := c.Query("status")
	if status != "active" && status != "inactive" {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "status must be active or inactive")
		return
	}
	if err := h.networks.SetNetworkStatus(c.Request.Context(), networkID, status); err != nil {
		mapServiceError(c, err)
		return
	}
	writeEmpty(c)
}
