// Package plans manages VPS plans and their version history.
package plans

import (
	"context"
	"errors"

	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
)

var (
	ErrConflict = errors.New("conflict")
	ErrNotFound = errors.New("not found")
)

type Service struct {
	plans *repositories.PlanRepository
	audit *audit.Service
}

func NewService(plans *repositories.PlanRepository, auditSvc *audit.Service) *Service {
	return &Service{plans: plans, audit: auditSvc}
}

func (s *Service) Create(ctx context.Context, p *models.Plan) (*models.Plan, error) {
	if p.Currency == "" {
		p.Currency = "USD"
	}
	created, err := s.plans.Create(ctx, p)
	if err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			return nil, ErrConflict
		}
		return nil, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "plan.create",
		ResourceType: "plan",
		ResourceID:   created.ID,
		Metadata:     map[string]any{"name": created.Name},
	})
	return created, nil
}

func (s *Service) List(ctx context.Context, activeOnly bool) ([]models.Plan, error) {
	return s.plans.List(ctx, activeOnly)
}

func (s *Service) Get(ctx context.Context, id string) (*models.Plan, error) {
	return s.plans.GetByID(ctx, id)
}

// Update snapshots the current plan into plan_versions and applies new
// values. Live subscriptions are unaffected.
func (s *Service) Update(ctx context.Context, planID string, p *models.Plan) (*models.Plan, error) {
	updated, err := s.plans.Update(ctx, planID, p)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "plan.update",
		ResourceType: "plan",
		ResourceID:   updated.ID,
		Metadata:     map[string]any{"version": updated.Version},
	})
	return updated, nil
}

func (s *Service) SetActive(ctx context.Context, planID string, active bool) error {
	if err := s.plans.SetActive(ctx, planID, active); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "plan.set_active",
		ResourceType: "plan",
		ResourceID:   planID,
		Metadata:     map[string]any{"active": active},
	})
	return nil
}

func (s *Service) ListVersions(ctx context.Context, planID string) ([]models.Plan, error) {
	return s.plans.ListVersions(ctx, planID)
}