// Package console implements short-lived, single-use console access tokens
// for VM VNC/serial consoles. The token flow authorizes access through the
// Control Plane before any connection to the agent's VNC endpoint.
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

// ConsoleInfo describes how to reach a VM's console.
type ConsoleInfo struct {
	ConsoleType string
	Host        string
	Port        int
}

// AgentConsoleClient is the agent API used to fetch console addressing.
type AgentConsoleClient interface {
	GetConsoleToken(ctx context.Context, vmID, vmName string) (host string, port int, token string, err error)
}

// Service issues and consumes console tokens.
type Service struct {
	tokens  *repositories.ConsoleTokenRepository
	agents  AgentConsoleClient
	ttl     time.Duration
	audit   *audit.Service
	baseURL string
}

func NewService(
	tokens *repositories.ConsoleTokenRepository,
	agents AgentConsoleClient,
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

// Issue requests a VNC console token for a VM owned by the user.
// The agent's VNC endpoint is never exposed directly to the user; the token
// authorizes the console gateway to proxy to it.
func (s *Service) Issue(ctx context.Context, userID, vmID, vmName string) (*models.ConsoleToken, string, error) {
	if s.agents == nil {
		return nil, "", errors.New("console backend not configured")
	}
	host, port, _, err := s.agents.GetConsoleToken(ctx, vmID, vmName)
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
		ConsoleType: "vnc",
		Host:        host,
		Port:        port,
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

// WebsocketURL builds the gateway websocket URL a noVNC client connects to.
func (s *Service) WebsocketURL(baseURL, token string) string {
	return fmt.Sprintf("%s/console/ws?token=%s", baseURL, token)
}

func generateToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate console token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}