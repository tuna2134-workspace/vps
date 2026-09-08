package repositories

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

type AuditRepository struct {
	db DBTX
}

func NewAuditRepository(db DBTX) *AuditRepository {
	return &AuditRepository{db: db}
}

// Record writes an audit entry. Callers MUST NOT include passwords, session
// tokens, keys, or PII in metadata.
func (r *AuditRepository) Record(ctx context.Context, a *models.AuditLog) error {
	meta, err := json.Marshal(a.Metadata)
	if err != nil {
		return fmt.Errorf("marshal audit metadata: %w", err)
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO audit_logs (user_id, action, resource_type, resource_id, ip, user_agent, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		nullableString(a.UserID), a.Action, a.ResourceType, a.ResourceID,
		nullableString(a.IP), a.UserAgent, meta)
	if err != nil {
		return fmt.Errorf("record audit log: %w", err)
	}
	return nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
