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

// Endpoint describes how to reach a VM's console. Exactly one of
// (Host, Port) or Path is set depending on the console type.
type Endpoint struct {
	ConsoleType string
	Host        string
	Port        int
	// PTY path used for serial consoles.
	Path string
}

// AgentClient is the agent API used to fetch console addressing.
type AgentClient interface {
	GetConsoleToken(ctx context.Context, endpoint, vmID, vmName, consoleType string) (*Endpoint, error)
}

// Service issues and consumes console tokens.
type Service struct {
	tokens  *repositories.ConsoleTokenRepository
	agents  AgentClient
	ttl     time.Duration
	audit   *audit.Service
	baseURL string
}

func NewService(
	tokens *repositories.ConsoleTokenRepository,
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
	ep, err := s.agents.GetConsoleToken(ctx, nodeEndpoint, vmID, vmName, consoleType)
	if err != nil {
		return nil, "", fmt.Errorf("request console endpoint: %w", err)
	}

	token, err := generateToken(32)
	if err != nil {
		return nil, "", err
	}
	tok, err := s.tokens.Create(ctx, &models.ConsoleToken{
		VMID:        vmID,
		UserID:      userID,
		TokenHash:   session.TokenFromString(token).Hash(),
		ConsoleType: consoleType,
		Host:        ep.Host,
		Port:        ep.Port,
		Path:        ep.Path,
		ExpiresAt:   time.Now().Add(s.ttl),
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

func generateToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate console token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
