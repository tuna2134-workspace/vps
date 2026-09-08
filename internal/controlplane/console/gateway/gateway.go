// Package gateway implements the console WebSocket gateway. A noVNC/serial
// client connects to /console/ws?token=...; the gateway validates the one-time
// token and bridges the WebSocket to the agent's streaming Console RPC (serial
// via virDomainOpenConsole, VNC via virDomainOpenGraphicsFD). Neither the VNC
// port nor the serial PTY is ever exposed on the network.
package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/tuna2134/vps/internal/controlplane/console"
	"github.com/tuna2134/vps/internal/controlplane/models"
)

// Gateway bridges authenticated WebSockets to VM consoles.
type Gateway struct {
	cons *console.Service
	log  *slog.Logger
}

func New(cons *console.Service, log *slog.Logger) *Gateway {
	return &Gateway{cons: cons, log: log}
}

// HandleWS is the HTTP handler for /console/ws. The one-time token is passed
// as a query parameter by the noVNC/serial client. The token is consumed
// atomically BEFORE the WebSocket upgrade, so invalid, expired, or reused
// tokens are rejected with 401 at the handshake.
func (g *Gateway) HandleWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing console token", http.StatusUnauthorized)
		return
	}
	tok, err := g.cons.Consume(r.Context(), token)
	if err != nil {
		g.log.Warn("console token rejected", "error", err)
		http.Error(w, "invalid, expired, or already-used console token", http.StatusUnauthorized)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: false,
		OriginPatterns:     []string{"*"},
	})
	if err != nil {
		g.log.Warn("websocket upgrade failed", "error", err)
		return
	}
	defer ws.Close(websocket.StatusInternalError, "session ended")

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
	defer cancel()

	if err := g.proxy(ctx, ws, tok); err != nil {
		g.log.Info("console websocket closed", "error", err)
	}
}

func (g *Gateway) proxy(ctx context.Context, ws *websocket.Conn, tok *models.ConsoleToken) error {
	agent := g.cons.Agent()
	if agent == nil {
		return errors.New("console agent backend not configured")
	}

	stream, err := agent.OpenConsole(ctx, tok.NodeEndpoint, tok.VMID, tok.VMName, tok.ConsoleType)
	if err != nil {
		g.log.Warn("open console stream failed", "vm", tok.VMID, "type", tok.ConsoleType, "error", err)
		return err
	}
	defer stream.Close()

	g.log.Info("console session established", "vm", tok.VMID, "type", tok.ConsoleType)

	errCh := make(chan error, 2)

	// WebSocket -> agent console
	go func() {
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				errCh <- err
				return
			}
			if typ != websocket.MessageBinary && typ != websocket.MessageText {
				continue
			}
			if err := stream.Send(data); err != nil {
				errCh <- err
				return
			}
		}
	}()

	// Agent console -> WebSocket
	go func() {
		for {
			data, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			if err := ws.Write(ctx, websocket.MessageBinary, data); err != nil {
				errCh <- err
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}
