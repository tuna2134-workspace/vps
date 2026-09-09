package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

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
// returns a one-time, expiring token plus the websocket URL a noVNC or serial
// client connects to. The agent's console endpoint is never returned to the
// user.
func (h *ConsoleHandlers) IssueVMConsole(c *gin.Context) {
	u := UserFrom(c)
	vm, err := h.vms.GetVM(c.Request.Context(), u.ID, c.Param("id"))
	if err != nil {
		mapError(c, err)
		return
	}
	if vm.Status != "running" {
		writeError(c, http.StatusConflict, "INVALID_STATE", "vm must be running to open a console")
		return
	}

	consoleType := console.TypeVNC
	var req consoleRequest
	if err := decodeJSON(c, &req); err == nil && req.ConsoleType != "" {
		consoleType = req.ConsoleType
	}
	if consoleType != console.TypeVNC && consoleType != console.TypeSerial {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "console_type must be vnc or serial")
		return
	}

	nodeEndpoint, err := h.vms.NodeEndpoint(c.Request.Context(), vm.NodeID)
	if err != nil {
		mapError(c, err)
		return
	}

	// The console token must carry the libvirt DOMAIN name (vps-<instance-id>),
	// not the VM's display name, so the agent can open the right domain.
	tok, rawToken, err := h.console.Issue(c.Request.Context(), u.ID, vm.ID, vms.DomainName(vm), nodeEndpoint, consoleType)
	if err != nil {
		mapError(c, err)
		return
	}
	wsURL := h.console.WebsocketURL(h.console.BaseURL(), rawToken)
	writeData(c, http.StatusOK, consoleResponse{
		ConsoleType:  tok.ConsoleType,
		Token:        rawToken,
		ExpiresAt:    tok.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		WebsocketURL: wsURL,
	})
}
