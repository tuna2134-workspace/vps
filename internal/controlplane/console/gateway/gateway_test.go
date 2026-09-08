package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tuna2134/vps/internal/controlplane/console"
	"github.com/tuna2134/vps/internal/controlplane/models"
)

// fakeAgent implements console.AgentClient over a pre-created pipe.
type fakeAgent struct {
	server net.Conn
}

func (f *fakeAgent) OpenConsole(ctx context.Context, endpoint, vmID, vmName, consoleType string) (console.Stream, error) {
	return &pipeStream{conn: f.server}, nil
}

// pipeStream adapts a net.Conn to console.Stream.
type pipeStream struct {
	conn net.Conn
}

func (p *pipeStream) Send(d []byte) error {
	_, err := p.conn.Write(d)
	return err
}

func (p *pipeStream) Recv() ([]byte, error) {
	buf := make([]byte, 32*1024)
	n, err := p.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func (p *pipeStream) Close() error { return p.conn.Close() }

// fakeConsumeService is a console.Service-like token consumer backed by the
// real console service with a stubbed agent and token store.
func newTestService(t *testing.T) (*console.Service, *fakeAgent, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	agent := &fakeAgent{server: server}
	return console.NewService(newMemTokenRepo(), agent, time.Minute, nil, "http://x"), agent, client
}

// TestGatewaySerialBridgesWebSocketToAgent verifies the gateway opens a console
// stream via the agent and pipes WebSocket data both ways.
func TestGatewaySerialBridgesWebSocketToAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	svc, _, client := newTestService(t)

	// Issue a serial token directly through the service.
	tok, rawToken, err := svc.Issue(ctx, "user-1", "vm-1", "vps-i-1", "agent:9001", console.TypeSerial)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if tok.NodeEndpoint != "agent:9001" {
		t.Errorf("node endpoint mismatch: %s", tok.NodeEndpoint)
	}

	// Start a fake console server on the client side of the pipe: it reads
	// input and echoes a marker back.
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := client.Read(buf)
			if err != nil {
				return
			}
			if string(buf[:n]) == "hello" {
				_, _ = client.Write([]byte("console-echo-ok\n"))
			}
		}
	}()

	// Serve the gateway and connect a WebSocket client.
	g := New(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(http.HandlerFunc(g.HandleWS))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):] + "/console/ws?token=" + rawToken
	ws, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: &http.Client{Timeout: 5 * time.Second}})
	if err != nil {
		t.Fatalf("dial ws: %v (status %v)", err, resp)
	}
	defer ws.Close(websocket.StatusNormalClosure, "done")

	// Send input; expect the console echo through the gateway.
	if err := ws.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(data) != "console-echo-ok\n" {
		t.Errorf("unexpected echo: %q", data)
	}
}

// TestGatewayRejectsReusedToken verifies single-use semantics.
func TestGatewayRejectsReusedToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	svc, _, _ := newTestService(t)
	_, rawToken, err := svc.Issue(ctx, "user-1", "vm-1", "vps-i-1", "agent:9001", console.TypeSerial)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	g := New(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(http.HandlerFunc(g.HandleWS))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):] + "/console/ws?token=" + rawToken
	ws1, resp1, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: &http.Client{Timeout: 5 * time.Second}})
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	if resp1 != nil && resp1.Body != nil {
		_ = resp1.Body.Close()
	}
	// Keep the first session open so the token is definitely consumed.
	defer ws1.Close(websocket.StatusNormalClosure, "done")

	// Second use of the same token must be rejected with 401.
	_, resp2, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: &http.Client{Timeout: 5 * time.Second}})
	if err == nil {
		t.Fatal("expected reused token to be rejected")
	}
	if resp2 == nil || resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %v (%v)", resp2, err)
	}
}

// memTokenRepo is a minimal in-memory token store for gateway tests.
type memTokenRepo struct {
	mu     sync.Mutex
	byHash map[string]*models.ConsoleToken
}

func newMemTokenRepo() *memTokenRepo {
	return &memTokenRepo{byHash: map[string]*models.ConsoleToken{}}
}

func (m *memTokenRepo) Create(ctx context.Context, t *models.ConsoleToken) (*models.ConsoleToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *t
	cp.ID = "tok-" + t.TokenHash[:8]
	m.byHash[t.TokenHash] = &cp
	return &cp, nil
}

func (m *memTokenRepo) Consume(ctx context.Context, tokenHash string) (*models.ConsoleToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.byHash[tokenHash]
	if !ok {
		return nil, errors.New("not found")
	}
	if t.UsedAt != nil || time.Now().After(t.ExpiresAt) {
		return nil, errors.New("used or expired")
	}
	now := time.Now()
	t.UsedAt = &now
	return t, nil
}

func (m *memTokenRepo) RevokeByVM(ctx context.Context, vmID string) error { return nil }
