// Package gateway implements the console WebSocket gateway. A noVNC/serial
// client connects to /console/ws?token=...; the gateway validates the one-time
// token and bridges the WebSocket to the VM's console endpoint. VNC endpoints
// are TCP, serial consoles are Unix domain sockets. Neither is ever exposed
// directly to clients.
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
	"github.com/tuna2134/vps/internal/controlplane/models"
)

// Gateway bridges authenticated WebSockets to console endpoints.
type Gateway struct {
	cons    *console.Service
	log     *slog.Logger
	timeout time.Duration
}

func New(cons *console.Service, log *slog.Logger) *Gateway {
	return &Gateway{cons: cons, log: log, timeout: 10 * time.Second}
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
	conn, err := g.dial(ctx, tok)
	if err != nil {
		g.log.Warn("console dial failed", "vm", tok.VMID, "type", tok.ConsoleType, "error", err)
		return err
	}
	defer conn.Close()

	g.log.Info("console session established", "vm", tok.VMID, "type", tok.ConsoleType)

	errCh := make(chan error, 2)

	// WebSocket -> console
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

	// Console -> WebSocket
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

// dial connects to the console endpoint: TCP for VNC, Unix socket for serial.
func (g *Gateway) dial(ctx context.Context, tok *models.ConsoleToken) (net.Conn, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	switch tok.ConsoleType {
	case console.TypeSerial:
		if tok.Path == "" {
			return nil, errors.New("serial console has no pty path")
		}
		d := net.Dialer{}
		return d.DialContext(timeoutCtx, "unix", tok.Path)
	default:
		if tok.Host == "" || tok.Port == 0 {
			return nil, errors.New("vnc console has no host/port")
		}
		addr := net.JoinHostPort(tok.Host, strconv.Itoa(tok.Port))
		d := net.Dialer{}
		return d.DialContext(timeoutCtx, "tcp", addr)
	}
}
