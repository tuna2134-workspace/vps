package api

import (
	"net/http"

	"github.com/tuna2134/vps/internal/controlplane/cluster"
)

type clusterRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type nodeRequest struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
	Endpoint  string `json:"agent_endpoint"`
}

type ClusterHandlers struct {
	cluster *cluster.Service
}

func NewClusterHandlers(clusterSvc *cluster.Service) *ClusterHandlers {
	return &ClusterHandlers{cluster: clusterSvc}
}

func (h *ClusterHandlers) ListClusters(w http.ResponseWriter, r *http.Request) {
	clusters, err := h.cluster.ListClusters(r.Context())
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, clusters)
}

func (h *ClusterHandlers) CreateCluster(w http.ResponseWriter, r *http.Request) {
	var req clusterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}
	c, err := h.cluster.CreateCluster(r.Context(), req.Name, req.Description)
	if err != nil {
		if err == cluster.ErrConflict {
			writeError(w, http.StatusConflict, "CONFLICT", "cluster already exists")
			return
		}
		mapError(w, err)
		return
	}
	writeData(w, http.StatusCreated, c)
}

func (h *ClusterHandlers) ListNodes(w http.ResponseWriter, r *http.Request) {
	clusterID := r.URL.Query().Get("cluster_id")
	nodes, err := h.cluster.ListNodes(r.Context(), clusterID)
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, nodes)
}

func (h *ClusterHandlers) RegisterNode(w http.ResponseWriter, r *http.Request) {
	var req nodeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.ClusterID == "" || req.Name == "" || req.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cluster_id, name and agent_endpoint are required")
		return
	}
	n, err := h.cluster.RegisterNode(r.Context(), req.ClusterID, req.Name, req.Endpoint)
	if err != nil {
		if err == cluster.ErrConflict {
			writeError(w, http.StatusConflict, "CONFLICT", "node already exists")
			return
		}
		mapError(w, err)
		return
	}
	writeData(w, http.StatusCreated, n)
}

func (h *ClusterHandlers) GetNode(w http.ResponseWriter, r *http.Request) {
	nodeID := pathSegment(r, "/v1/nodes/")
	n, err := h.cluster.GetNode(r.Context(), nodeID)
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, n)
}

func (h *ClusterHandlers) ListStoragePools(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node_id")
	pools, err := h.cluster.ListStoragePools(r.Context(), nodeID)
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, pools)
}
