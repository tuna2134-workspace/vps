// Package gateway implements the console WebSocket gateway. A noVNC client
// connects to /console/ws?token=...; the gateway validates the one-time token
// and bridges the WebSocket to the VM's VNC TCP endpoint. The VNC server is
// never exposed directly to clients.
package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/tuna2134/vps/internal/controlplane/console"
)

// Gateway bridges authenticated WebSockets to VNC TCP endpoints.
type Gateway struct {
	cons    *console.Service
	log     *slog.Logger
	timeout time.Duration
}

func New(cons *console.Service, log *slog.Logger) *Gateway {
	return &Gateway{cons: cons, log: log, timeout: 10 * time.Second}
}

// HandleWS is the HTTP handler for /console/ws. The one-time token is passed
// as a query parameter by the noVNC client.
func (g *Gateway) HandleWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing console token", http.StatusUnauthorized)
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

	if err := g.proxy(ctx, ws, token); err != nil {
		g.log.Info("console websocket closed", "error", err)
	}
}

func (g *Gateway) proxy(ctx context.Context, ws *websocket.Conn, rawToken string) error {
	tok, err := g.cons.Consume(ctx, rawToken)
	if err != nil {
		g.log.Warn("console token rejected", "error", err)
		return errors.New("invalid or expired console token")
	}

	addr := net.JoinHostPort(tok.Host, strconv.Itoa(tok.Port))
	conn, err := net.DialTimeout("tcp", addr, g.timeout)
	if err != nil {
		g.log.Warn("console tcp dial failed", "vm", tok.VMID, "error", err)
		return err
	}
	defer conn.Close()

	g.log.Info("console session established", "vm", tok.VMID, "target", addr)

	errCh := make(chan error, 2)

	// WebSocket -> VNC
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
			if _, err := conn.Write(data); err != nil {
				errCh <- err
				return
			}
		}
	}()

	// VNC -> WebSocket
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				errCh <- err
				return
			}
			if err := ws.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
				errCh <- err
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}
