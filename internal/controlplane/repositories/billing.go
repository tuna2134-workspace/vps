package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

type BillingRepository struct {
	db DBTX
}

func NewBillingRepository(db DBTX) *BillingRepository {
	return &BillingRepository{db: db}
}

func (r *BillingRepository) UpsertCustomer(ctx context.Context, userID, stripeCustomerID string) (*models.BillingCustomer, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO billing_customers (user_id, stripe_customer_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET stripe_customer_id = EXCLUDED.stripe_customer_id, updated_at = now()
		RETURNING id, user_id, stripe_customer_id, created_at, updated_at`,
		userID, stripeCustomerID)
	var c models.BillingCustomer
	if err := row.Scan(&c.ID, &c.UserID, &c.StripeCustomerID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("upsert billing customer: %w", err)
	}
	return &c, nil
}

func (r *BillingRepository) GetCustomerByUser(ctx context.Context, userID string) (*models.BillingCustomer, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, stripe_customer_id, created_at, updated_at
		FROM billing_customers WHERE user_id = $1`, userID)
	var c models.BillingCustomer
	if err := row.Scan(&c.ID, &c.UserID, &c.StripeCustomerID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get billing customer: %w", err)
	}
	return &c, nil
}

func (r *BillingRepository) GetUserByStripeCustomer(ctx context.Context, stripeCustomerID string) (*models.BillingCustomer, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, stripe_customer_id, created_at, updated_at
		FROM billing_customers WHERE stripe_customer_id = $1`, stripeCustomerID)
	var c models.BillingCustomer
	if err := row.Scan(&c.ID, &c.UserID, &c.StripeCustomerID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get billing customer by stripe id: %w", err)
	}
	return &c, nil
}

func (r *BillingRepository) UpsertSubscription(ctx context.Context, s *models.Subscription) (*models.Subscription, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO subscriptions (user_id, vm_id, plan_id, stripe_subscription_id, status, billing_status,
			current_period_start, current_period_end)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (stripe_subscription_id) DO UPDATE SET
			status = EXCLUDED.status,
			billing_status = EXCLUDED.billing_status,
			current_period_start = EXCLUDED.current_period_start,
			current_period_end = EXCLUDED.current_period_end,
			vm_id = EXCLUDED.vm_id,
			updated_at = now()
		RETURNING id, user_id, vm_id, plan_id, stripe_subscription_id, status, billing_status,
			current_period_start, current_period_end, created_at, updated_at`,
		s.UserID, s.VMID, s.PlanID, s.StripeSubscriptionID, s.Status, s.BillingStatus,
		s.CurrentPeriodStart, s.CurrentPeriodEnd)
	var out models.Subscription
	var vmID, planID *string
	if err := row.Scan(&out.ID, &out.UserID, &vmID, &planID, &out.StripeSubscriptionID,
		&out.Status, &out.BillingStatus, &out.CurrentPeriodStart, &out.CurrentPeriodEnd,
		&out.CreatedAt, &out.UpdatedAt); err != nil {
		return nil, fmt.Errorf("upsert subscription: %w", err)
	}
	if vmID != nil {
		out.VMID = *vmID
	}
	if planID != nil {
		out.PlanID = *planID
	}
	return &out, nil
}

func (r *BillingRepository) GetSubscriptionByStripeID(ctx context.Context, stripeSubID string) (*models.Subscription, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, vm_id, plan_id, stripe_subscription_id, status, billing_status,
			current_period_start, current_period_end, created_at, updated_at
		FROM subscriptions WHERE stripe_subscription_id = $1`, stripeSubID)
	var s models.Subscription
	var vmID, planID *string
	if err := row.Scan(&s.ID, &s.UserID, &vmID, &planID, &s.StripeSubscriptionID,
		&s.Status, &s.BillingStatus, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
		&s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	if vmID != nil {
		s.VMID = *vmID
	}
	if planID != nil {
		s.PlanID = *planID
	}
	return &s, nil
}

func (r *BillingRepository) GetSubscriptionByUser(ctx context.Context, userID string) (*models.Subscription, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, vm_id, plan_id, stripe_subscription_id, status, billing_status,
			current_period_start, current_period_end, created_at, updated_at
		FROM subscriptions WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`, userID)
	var s models.Subscription
	var vmID, planID *string
	if err := row.Scan(&s.ID, &s.UserID, &vmID, &planID, &s.StripeSubscriptionID,
		&s.Status, &s.BillingStatus, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
		&s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get subscription by user: %w", err)
	}
	if vmID != nil {
		s.VMID = *vmID
	}
	if planID != nil {
		s.PlanID = *planID
	}
	return &s, nil
}

func (r *BillingRepository) SetSubscriptionBillingStatus(ctx context.Context, stripeSubID, status string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE subscriptions SET billing_status = $2, updated_at = now() WHERE stripe_subscription_id = $1`,
		stripeSubID, status)
	if err != nil {
		return fmt.Errorf("set subscription billing status: %w", err)
	}
	return nil
}

func (r *BillingRepository) InsertInvoice(ctx context.Context, inv *models.Invoice) (*models.Invoice, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO invoices (user_id, stripe_invoice_id, amount_cents, currency, status, paid_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (stripe_invoice_id) DO UPDATE SET
			status = EXCLUDED.status, paid_at = EXCLUDED.paid_at
		RETURNING id, user_id, stripe_invoice_id, amount_cents, currency, status, paid_at, created_at`,
		inv.UserID, inv.StripeInvoiceID, inv.AmountCents, inv.Currency, inv.Status, inv.PaidAt)
	var out models.Invoice
	if err := row.Scan(&out.ID, &out.UserID, &out.StripeInvoiceID, &out.AmountCents, &out.Currency,
		&out.Status, &out.PaidAt, &out.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert invoice: %w", err)
	}
	return &out, nil
}

func (r *BillingRepository) ListInvoicesByUser(ctx context.Context, userID string) ([]models.Invoice, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, stripe_invoice_id, amount_cents, currency, status, paid_at, created_at
		FROM invoices WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list invoices: %w", err)
	}
	defer rows.Close()
	var out []models.Invoice
	for rows.Next() {
		var inv models.Invoice
		if err := rows.Scan(&inv.ID, &inv.UserID, &inv.StripeInvoiceID, &inv.AmountCents,
			&inv.Currency, &inv.Status, &inv.PaidAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (r *BillingRepository) InsertPayment(ctx context.Context, p *models.Payment) (*models.Payment, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO payments (user_id, stripe_payment_intent_id, amount_cents, currency, status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (stripe_payment_intent_id) DO UPDATE SET status = EXCLUDED.status
		RETURNING id, user_id, stripe_payment_intent_id, amount_cents, currency, status, created_at`,
		p.UserID, p.StripePaymentIntentID, p.AmountCents, p.Currency, p.Status)
	var out models.Payment
	if err := row.Scan(&out.ID, &out.UserID, &out.StripePaymentIntentID, &out.AmountCents,
		&out.Currency, &out.Status, &out.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert payment: %w", err)
	}
	return &out, nil
}

func (r *BillingRepository) GetPaymentByIntentID(ctx context.Context, intentID string) (*models.Payment, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, stripe_payment_intent_id, amount_cents, currency, status, created_at
		FROM payments WHERE stripe_payment_intent_id = $1`, intentID)
	var p models.Payment
	if err := row.Scan(&p.ID, &p.UserID, &p.StripePaymentIntentID, &p.AmountCents,
		&p.Currency, &p.Status, &p.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get payment: %w", err)
	}
	return &p, nil
}

// MarkWebhookProcessed records a Stripe event id so it is never processed twice.
// Returns false if the event was already processed (idempotent).
func (r *BillingRepository) MarkWebhookProcessed(ctx context.Context, stripeEventID, eventType string) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		INSERT INTO webhook_events (stripe_event_id, event_type)
		VALUES ($1, $2) ON CONFLICT (stripe_event_id) DO NOTHING`,
		stripeEventID, eventType)
	if err != nil {
		return false, fmt.Errorf("mark webhook processed: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}