package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

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

func (h *ClusterHandlers) ListClusters(c *gin.Context) {
	clusters, err := h.cluster.ListClusters(c.Request.Context())
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, clusters)
}

func (h *ClusterHandlers) CreateCluster(c *gin.Context) {
	var req clusterRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}
	cl, err := h.cluster.CreateCluster(c.Request.Context(), req.Name, req.Description)
	if err != nil {
		if err == cluster.ErrConflict {
			writeError(c, http.StatusConflict, "CONFLICT", "cluster already exists")
			return
		}
		mapError(c, err)
		return
	}
	writeData(c, http.StatusCreated, cl)
}

func (h *ClusterHandlers) ListNodes(c *gin.Context) {
	nodes, err := h.cluster.ListNodes(c.Request.Context(), c.Query("cluster_id"))
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, nodes)
}

func (h *ClusterHandlers) RegisterNode(c *gin.Context) {
	var req nodeRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.ClusterID == "" || req.Name == "" || req.Endpoint == "" {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "cluster_id, name and agent_endpoint are required")
		return
	}
	n, err := h.cluster.RegisterNode(c.Request.Context(), req.ClusterID, req.Name, req.Endpoint)
	if err != nil {
		if err == cluster.ErrConflict {
			writeError(c, http.StatusConflict, "CONFLICT", "node already exists")
			return
		}
		mapError(c, err)
		return
	}
	writeData(c, http.StatusCreated, n)
}

func (h *ClusterHandlers) GetNode(c *gin.Context) {
	nodeID := c.Param("id")
	n, err := h.cluster.GetNode(c.Request.Context(), nodeID)
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, n)
}

func (h *ClusterHandlers) ListStoragePools(c *gin.Context) {
	pools, err := h.cluster.ListStoragePools(c.Request.Context(), c.Query("node_id"))
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, pools)
}
