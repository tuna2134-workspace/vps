// Package audit provides a thin wrapper around the audit log repository. It
// intentionally exposes a small surface so secrets are never accidentally
// recorded.
package audit

import (
	"context"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
)

type Service struct {
	repo *repositories.AuditRepository
}

func NewService(repo *repositories.AuditRepository) *Service {
	return &Service{repo: repo}
}

// Event carries the fields for an audit log entry.
type Event struct {
	UserID       string
	Action       string
	ResourceType string
	ResourceID   string
	IP           string
	UserAgent    string
	Metadata     map[string]any
}

func (s *Service) Record(ctx context.Context, e Event) error {
	if s == nil || s.repo == nil {
		return nil
	}
	return s.repo.Record(ctx, &models.AuditLog{
		UserID:       e.UserID,
		Action:       e.Action,
		ResourceType: e.ResourceType,
		ResourceID:   e.ResourceID,
		IP:           e.IP,
		UserAgent:    e.UserAgent,
		Metadata:     e.Metadata,
	})
}
