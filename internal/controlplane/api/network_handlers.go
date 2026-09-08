package api

import (
	"net/http"

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
	CIDR   string `json:"cidr"`
	Type   string `json:"type"`
	Gateway string `json:"gateway"`
}

type NetworkHandlers struct {
	networks *networks.Service
}

func NewNetworkHandlers(netSvc *networks.Service) *NetworkHandlers {
	return &NetworkHandlers{networks: netSvc}
}

func (h *NetworkHandlers) List(w http.ResponseWriter, r *http.Request) {
	n, err := h.networks.ListNetworks(r.Context(), r.URL.Query().Get("active") != "false")
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, n)
}

func (h *NetworkHandlers) Get(w http.ResponseWriter, r *http.Request) {
	n, err := h.networks.GetNetwork(r.Context(), pathSegment(r, "/v1/networks/"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, n)
}

func (h *NetworkHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var req networkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Name == "" || req.Bridge == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name and bridge are required")
		return
	}
	created, err := h.networks.CreateNetwork(r.Context(), &models.Network{
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
			writeError(w, http.StatusConflict, "CONFLICT", "network already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	writeData(w, http.StatusCreated, created)
}

func (h *NetworkHandlers) AddPool(w http.ResponseWriter, r *http.Request) {
	networkID := pathSegment(r, "/v1/networks/")
	var req poolRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.CIDR == "" || (req.Type != "ipv4" && req.Type != "ipv6") {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cidr and type (ipv4|ipv6) are required")
		return
	}
	created, err := h.networks.AddPool(r.Context(), &models.IPPool{
		NetworkID: networkID,
		CIDR:      req.CIDR,
		Type:      req.Type,
		Gateway:   req.Gateway,
	})
	if err != nil {
		if err == networks.ErrConflict {
			writeError(w, http.StatusConflict, "CONFLICT", "pool already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	writeData(w, http.StatusCreated, created)
}

func (h *NetworkHandlers) ListPools(w http.ResponseWriter, r *http.Request) {
	networkID := pathSegment(r, "/v1/networks/")
	pools, err := h.networks.ListPools(r.Context(), networkID)
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, pools)
}

func (h *NetworkHandlers) SetStatus(w http.ResponseWriter, r *http.Request) {
	networkID := pathSegment(r, "/v1/networks/")
	status := r.URL.Query().Get("status")
	if status != "active" && status != "inactive" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "status must be active or inactive")
		return
	}
	if err := h.networks.SetNetworkStatus(r.Context(), networkID, status); err != nil {
		mapServiceError(w, err)
		return
	}
	writeEmpty(w)
}