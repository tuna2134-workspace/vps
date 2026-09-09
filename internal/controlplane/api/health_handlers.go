package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Health provides liveness/readiness endpoints.
type Health struct {
	// Ready returns nil when the service is ready.
	Ready func(ctx context.Context) error
}

func (h *Health) Liveness(c *gin.Context) {
	writeData(c, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Health) Readiness(c *gin.Context) {
	if h.Ready != nil {
		if err := h.Ready(c.Request.Context()); err != nil {
			writeError(c, http.StatusServiceUnavailable, "NOT_READY", "service is not ready")
			return
		}
	}
	writeData(c, http.StatusOK, map[string]string{"status": "ok"})
}
