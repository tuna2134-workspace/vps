package api

import (
	"context"
	"net/http"
)

// Health provides liveness/readiness endpoints.
type Health struct {
	// Ready returns nil when the service is ready.
	Ready func(ctx context.Context) error
}

func (h *Health) Liveness(w http.ResponseWriter, _ *http.Request) {
	writeData(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Health) Readiness(w http.ResponseWriter, r *http.Request) {
	if h.Ready != nil {
		if err := h.Ready(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "NOT_READY", "service is not ready")
			return
		}
	}
	writeData(w, http.StatusOK, map[string]string{"status": "ok"})
}