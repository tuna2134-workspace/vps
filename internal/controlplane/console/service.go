// Package console implements short-lived, single-use console access tokens
// for VM VNC/serial consoles. The token flow authorizes access through the
// Control Plane before any connection to the agent's console endpoint.
package console

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
	"github.com/tuna2134/vps/internal/controlplane/session"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExpired  = errors.New("token expired or already used")
)

// Console type constants.
const (
	TypeVNC    = "vnc"
	TypeSerial = "serial"
)

// Stream is a bidirectional byte stream to a VM's console (serial or VNC),
// backed by the agent's gRPC Console RPC (virDomainOpenConsole /
// virDomainOpenGraphicsFD).
type Stream interface {
	Send([]byte) error
	Recv() ([]byte, error)
	Close() error
}

// AgentClient is the agent API used to open console streams.
type AgentClient interface {
	// OpenConsole opens a bidirectional stream to a VM's console. consoleType
	// is "vnc" or "serial".
	OpenConsole(ctx context.Context, endpoint, vmID, vmName, consoleType string) (Stream, error)
}

// tokenStore persists console tokens. The repository implementation satisfies
// this interface; tests can inject an in-memory store.
type tokenStore interface {
	Create(ctx context.Context, t *models.ConsoleToken) (*models.ConsoleToken, error)
	Consume(ctx context.Context, tokenHash string) (*models.ConsoleToken, error)
}

var _ tokenStore = (*repositories.ConsoleTokenRepository)(nil)

// Service issues and consumes console tokens.
type Service struct {
	tokens  tokenStore
	agents  AgentClient
	ttl     time.Duration
	audit   *audit.Service
	baseURL string
}

func NewService(
	tokens tokenStore,
	agents AgentClient,
	ttl time.Duration,
	auditSvc *audit.Service,
	publicBaseURL string,
) *Service {
	return &Service{
		tokens:  tokens,
		agents:  agents,
		ttl:     ttl,
		audit:   auditSvc,
		baseURL: publicBaseURL,
	}
}

// Issue requests a console token for a VM owned by the user. The agent's
// console endpoint is never exposed to the user; the token authorizes the
// console gateway to proxy to it.
func (s *Service) Issue(ctx context.Context, userID, vmID, vmName, nodeEndpoint, consoleType string) (*models.ConsoleToken, string, error) {
	if s.agents == nil {
		return nil, "", errors.New("console backend not configured")
	}
	if nodeEndpoint == "" {
		return nil, "", errors.New("vm has no agent endpoint")
	}
	if consoleType == "" {
		consoleType = TypeVNC
	}
	if consoleType != TypeVNC && consoleType != TypeSerial {
		return nil, "", fmt.Errorf("unsupported console type: %s", consoleType)
	}

	// The console stream is opened by the gateway on demand (serial via
	// virDomainOpenConsole, VNC via virDomainOpenGraphicsFD); the token only
	// records how to reach the agent.
	token, err := generateToken(32)
	if err != nil {
		return nil, "", err
	}
	tok, err := s.tokens.Create(ctx, &models.ConsoleToken{
		VMID:         vmID,
		UserID:       userID,
		TokenHash:    session.TokenFromString(token).Hash(),
		ConsoleType:  consoleType,
		NodeEndpoint: nodeEndpoint,
		VMName:       vmName,
		ExpiresAt:    time.Now().Add(s.ttl),
	})
	if err != nil {
		return nil, "", err
	}
	_ = s.audit.Record(ctx, audit.Event{
		UserID:       userID,
		Action:       "console.token_issued",
		ResourceType: "vm",
		ResourceID:   vmID,
		Metadata:     map[string]any{"console_type": consoleType},
	})
	return tok, token, nil
}

// Consume validates a one-time console token and returns the target endpoint.
func (s *Service) Consume(ctx context.Context, token string) (*models.ConsoleToken, error) {
	tok, err := s.tokens.Consume(ctx, session.TokenFromString(token).Hash())
	if err != nil {
		return nil, ErrNotFound
	}
	return tok, nil
}

// WebsocketURL builds the gateway websocket URL a noVNC/console client
// connects to.
func (s *Service) WebsocketURL(baseURL, token string) string {
	return fmt.Sprintf("%s/console/ws?token=%s", baseURL, token)
}

// BaseURL returns the configured public base URL.
func (s *Service) BaseURL() string {
	return s.baseURL
}

// Agent returns the configured agent client (used by the gateway to open
// serial console streams).
func (s *Service) Agent() AgentClient {
	return s.agents
}

func generateToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate console token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
