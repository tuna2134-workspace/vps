// Package images manages base OS image records.
package images

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
	images *repositories.ImageRepository
	audit  *audit.Service
}

func NewService(images *repositories.ImageRepository, auditSvc *audit.Service) *Service {
	return &Service{images: images, audit: auditSvc}
}

func (s *Service) Create(ctx context.Context, img *models.Image) (*models.Image, error) {
	if img.Format == "" {
		img.Format = "qcow2"
	}
	if img.Architecture == "" {
		img.Architecture = "x86_64"
	}
	created, err := s.images.Create(ctx, img)
	if err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			return nil, ErrConflict
		}
		return nil, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "image.create",
		ResourceType: "image",
		ResourceID:   created.ID,
		Metadata:     map[string]any{"name": created.Name, "version": created.Version},
	})
	return created, nil
}

func (s *Service) List(ctx context.Context, activeOnly bool) ([]models.Image, error) {
	return s.images.List(ctx, activeOnly)
}

func (s *Service) Get(ctx context.Context, id string) (*models.Image, error) {
	return s.images.GetByID(ctx, id)
}
