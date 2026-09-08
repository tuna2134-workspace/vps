package api

import (
	"net/http"

	"github.com/tuna2134/vps/internal/controlplane/console"
	"github.com/tuna2134/vps/internal/controlplane/vms"
)

type consoleRequest struct {
	ConsoleType string `json:"console_type"` // "vnc" (default) or "serial"
}

type consoleResponse struct {
	ConsoleType  string `json:"console_type"`
	Token        string `json:"token"`
	ExpiresAt    string `json:"expires_at"`
	WebsocketURL string `json:"websocket_url"`
}

// ConsoleHandlers issues authorized console tokens for VM owners.
type ConsoleHandlers struct {
	console *console.Service
	vms     *vms.Service
}

func NewConsoleHandlers(consoleSvc *console.Service, vmSvc *vms.Service) *ConsoleHandlers {
	return &ConsoleHandlers{console: consoleSvc, vms: vmSvc}
}

// IssueVMConsole is POST /v1/vms/{id}/console. It verifies VM ownership then
// returns a one-time, expiring token plus the websocket URL a noVNC client
// connects to. The agent's VNC port is never returned to the user.
func (h *ConsoleHandlers) IssueVMConsole(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	vmID := pathSegment(r, "/v1/vms/")
	if idx := indexLastSlash(vmID); idx >= 0 {
		vmID = vmID[:idx]
	}
	vm, err := h.vms.GetVM(r.Context(), u.ID, vmID)
	if err != nil {
		mapError(w, err)
		return
	}
	if vm.Status != "running" {
		writeError(w, http.StatusConflict, "INVALID_STATE", "vm must be running to open a console")
		return
	}

	nodeEndpoint, err := h.vms.NodeEndpoint(r.Context(), vm.NodeID)
	if err != nil {
		mapError(w, err)
		return
	}

	tok, rawToken, err := h.console.Issue(r.Context(), u.ID, vm.ID, vm.Name, nodeEndpoint)
	if err != nil {
		mapError(w, err)
		return
	}
	wsURL := h.console.WebsocketURL(h.console.BaseURL(), rawToken)
	writeData(w, http.StatusOK, consoleResponse{
		ConsoleType:  tok.ConsoleType,
		Token:        rawToken,
		ExpiresAt:    tok.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		WebsocketURL: wsURL,
	})
}

func indexLastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}
