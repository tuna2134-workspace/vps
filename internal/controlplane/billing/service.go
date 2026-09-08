// Package billing integrates Stripe for payments, subscriptions, invoices,
// and webhooks. All webhook handling is idempotent.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/client"
	"github.com/stripe/stripe-go/v81/subscription"
	"github.com/stripe/stripe-go/v81/webhook"

	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrNotConfigured = errors.New("stripe is not configured")
)

// Service wraps the Stripe API.
type Service struct {
	client        *client.API
	webhookSecret string
	repo          *repositories.BillingRepository
	audit         *audit.Service
	log           *slog.Logger
	// OnSubscriptionActive is invoked (in-process) when a subscription becomes
	// active so a VM can be provisioned.
	OnSubscriptionActive func(ctx context.Context, userID string) error
	// OnSubscriptionCanceled handles cancellation (schedule termination).
	OnSubscriptionCanceled func(ctx context.Context, stripeSubID string) error
	// OnPaymentFailed suspends the affected VM.
	OnPaymentFailed func(ctx context.Context, userID string) error
}

func NewService(
	secretKey, webhookSecret string,
	repo *repositories.BillingRepository,
	auditSvc *audit.Service,
	log *slog.Logger,
) *Service {
	var sc *client.API
	if secretKey != "" {
		sc = client.New(secretKey, nil)
	}
	return &Service{
		client:        sc,
		webhookSecret: webhookSecret,
		repo:          repo,
		audit:         auditSvc,
		log:           log,
	}
}

func (s *Service) configured() error {
	if s.client == nil {
		return ErrNotConfigured
	}
	return nil
}

// GetOrCreateCustomer returns (creating if needed) the Stripe customer for a
// user.
func (s *Service) GetOrCreateCustomer(ctx context.Context, userID, email string) (*models.BillingCustomer, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if existing, err := s.repo.GetCustomerByUser(ctx, userID); err == nil {
		return existing, nil
	}

	cust, err := s.client.Customers.New(&stripe.CustomerParams{
		Email: stripe.String(email),
	})
	if err != nil {
		return nil, fmt.Errorf("create stripe customer: %w", err)
	}
	c, err := s.repo.UpsertCustomer(ctx, userID, cust.ID)
	if err != nil {
		return nil, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		UserID:       userID,
		Action:       "billing.customer_created",
		ResourceType: "billing",
		ResourceID:   cust.ID,
	})
	return c, nil
}

// CreateSubscription starts a Stripe subscription for the user at the given
// price. In practice the checkout flow provides the payment method; this
// returns the new subscription id.
func (s *Service) CreateSubscription(ctx context.Context, userID, stripeCustomerID, stripePriceID string) (*models.Subscription, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	params := &stripe.SubscriptionParams{
		Customer: stripe.String(stripeCustomerID),
		Items: []*stripe.SubscriptionItemsParams{{
			Price: stripe.String(stripePriceID),
		}},
		PaymentSettings: &stripe.SubscriptionPaymentSettingsParams{
			SaveDefaultPaymentMethod: stripe.String(string(stripe.SubscriptionPaymentSettingsSaveDefaultPaymentMethodOnSubscription)),
		},
	}
	sub, err := subscription.New(params)
	if err != nil {
		return nil, fmt.Errorf("create stripe subscription: %w", err)
	}
	return s.upsertSubscription(ctx, userID, sub, "")
}

func (s *Service) upsertSubscription(ctx context.Context, userID string, sub *stripe.Subscription, vmID string) (*models.Subscription, error) {
	var periodStart, periodEnd *time.Time
	if sub.CurrentPeriodStart > 0 {
		t := time.Unix(sub.CurrentPeriodStart, 0).UTC()
		periodStart = &t
	}
	if sub.CurrentPeriodEnd > 0 {
		t := time.Unix(sub.CurrentPeriodEnd, 0).UTC()
		periodEnd = &t
	}
	status := string(sub.Status)
	billingStatus := "active"
	switch sub.Status {
	case stripe.SubscriptionStatusPastDue:
		billingStatus = "past_due"
	case stripe.SubscriptionStatusUnpaid:
		billingStatus = "unpaid"
	case stripe.SubscriptionStatusCanceled:
		billingStatus = "canceled"
	}
	saved, err := s.repo.UpsertSubscription(ctx, &models.Subscription{
		UserID:               userID,
		VMID:                 vmID,
		StripeSubscriptionID: sub.ID,
		Status:               status,
		BillingStatus:        billingStatus,
		CurrentPeriodStart:   periodStart,
		CurrentPeriodEnd:     periodEnd,
	})
	if err != nil {
		return nil, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		UserID:       userID,
		Action:       "billing.subscription_updated",
		ResourceType: "billing",
		ResourceID:   sub.ID,
		Metadata:     map[string]any{"status": status},
	})
	return saved, nil
}

// HandleWebhook verifies and dispatches a Stripe webhook event idempotently.
// It returns (false, nil) when the event was already processed.
func (s *Service) HandleWebhook(ctx context.Context, payload []byte, signatureHeader string) (bool, error) {
	if s.webhookSecret == "" {
		return false, ErrNotConfigured
	}
	event, err := webhook.ConstructEvent(payload, signatureHeader, s.webhookSecret)
	if err != nil {
		return false, fmt.Errorf("invalid stripe webhook signature: %w", err)
	}

	// Idempotency: skip events already processed.
	processed, err := s.repo.MarkWebhookProcessed(ctx, event.ID, string(event.Type))
	if err != nil {
		return false, err
	}
	if !processed {
		s.log.Info("stripe webhook already processed", "event_id", event.ID)
		return false, nil
	}

	switch event.Type {
	case stripe.EventTypeCustomerSubscriptionCreated:
		err = s.handleSubscriptionCreated(ctx, event)
	case stripe.EventTypeCustomerSubscriptionUpdated:
		err = s.handleSubscriptionUpdated(ctx, event)
	case stripe.EventTypeCustomerSubscriptionDeleted:
		err = s.handleSubscriptionDeleted(ctx, event)
	case stripe.EventTypeInvoicePaymentSucceeded:
		err = s.handleInvoicePaymentSucceeded(ctx, event)
	case stripe.EventTypeInvoicePaymentFailed:
		err = s.handleInvoicePaymentFailed(ctx, event)
	case stripe.EventTypePaymentIntentSucceeded:
		err = s.handlePaymentIntentSucceeded(ctx, event)
	default:
		// Ack unknown events; they require no action.
		s.log.Info("stripe webhook ignored", "type", event.Type)
	}
	if err != nil {
		return true, fmt.Errorf("process stripe event %s: %w", event.Type, err)
	}
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "billing.webhook",
		ResourceType: "billing",
		ResourceID:   event.ID,
		Metadata:     map[string]any{"type": string(event.Type)},
	})
	return true, nil
}

func (s *Service) handleSubscriptionCreated(ctx context.Context, event stripe.Event) error {
	sub := &stripe.Subscription{}
	if err := jsonUnmarshal(event.Data.Raw, sub); err != nil {
		return err
	}
	return s.syncSubscription(ctx, sub)
}

func (s *Service) handleSubscriptionUpdated(ctx context.Context, event stripe.Event) error {
	sub := &stripe.Subscription{}
	if err := jsonUnmarshal(event.Data.Raw, sub); err != nil {
		return err
	}
	return s.syncSubscription(ctx, sub)
}

func (s *Service) handleSubscriptionDeleted(ctx context.Context, event stripe.Event) error {
	sub := &stripe.Subscription{}
	if err := jsonUnmarshal(event.Data.Raw, sub); err != nil {
		return err
	}
	if saved, err := s.repo.UpsertSubscription(ctx, &models.Subscription{
		UserID:               customerUserID(ctx, s, sub.Customer.ID),
		StripeSubscriptionID: sub.ID,
		Status:               "canceled",
		BillingStatus:        "canceled",
	}); err != nil {
		return err
	} else if s.OnSubscriptionCanceled != nil {
		_ = s.audit.Record(ctx, audit.Event{
			Action:       "billing.subscription_canceled",
			ResourceType: "billing",
			ResourceID:   sub.ID,
		})
		return s.OnSubscriptionCanceled(ctx, saved.StripeSubscriptionID)
	}
	return nil
}

func (s *Service) handleInvoicePaymentSucceeded(ctx context.Context, event stripe.Event) error {
	inv := &stripe.Invoice{}
	if err := jsonUnmarshal(event.Data.Raw, inv); err != nil {
		return err
	}
	userID := s.lookupUserByCustomer(ctx, inv.Customer.ID)
	if userID == "" {
		return fmt.Errorf("no local user for stripe customer %s", inv.Customer.ID)
	}
	paidAt := unixTime(inv.StatusTransitions.PaidAt)
	_, err := s.repo.InsertInvoice(ctx, &models.Invoice{
		UserID:          userID,
		StripeInvoiceID: inv.ID,
		AmountCents:     int(inv.AmountDue),
		Currency:        string(inv.Currency),
		Status:          string(inv.Status),
		PaidAt:          paidAt,
	})
	if err != nil {
		return err
	}
	// The successful payment may activate an incomplete subscription and
	// therefore allow VM provisioning.
	if s.OnSubscriptionActive != nil {
		return s.OnSubscriptionActive(ctx, userID)
	}
	return nil
}

func (s *Service) handleInvoicePaymentFailed(ctx context.Context, event stripe.Event) error {
	inv := &stripe.Invoice{}
	if err := jsonUnmarshal(event.Data.Raw, inv); err != nil {
		return err
	}
	userID := s.lookupUserByCustomer(ctx, inv.Customer.ID)
	if userID == "" {
		return fmt.Errorf("no local user for stripe customer %s", inv.Customer.ID)
	}
	if s.OnPaymentFailed != nil {
		return s.OnPaymentFailed(ctx, userID)
	}
	return nil
}

func (s *Service) handlePaymentIntentSucceeded(ctx context.Context, event stripe.Event) error {
	pi := &stripe.PaymentIntent{}
	if err := jsonUnmarshal(event.Data.Raw, pi); err != nil {
		return err
	}
	userID := s.lookupUserByCustomer(ctx, pi.Customer.ID)
	if userID == "" {
		return fmt.Errorf("no local user for stripe customer %s", pi.Customer.ID)
	}
	_, err := s.repo.InsertPayment(ctx, &models.Payment{
		UserID:                userID,
		StripePaymentIntentID: pi.ID,
		AmountCents:           int(pi.Amount),
		Currency:              string(pi.Currency),
		Status:                string(pi.Status),
	})
	if err != nil {
		return err
	}
	if s.OnSubscriptionActive != nil {
		return s.OnSubscriptionActive(ctx, userID)
	}
	return nil
}

func (s *Service) syncSubscription(ctx context.Context, sub *stripe.Subscription) error {
	userID := s.lookupUserByCustomer(ctx, sub.Customer.ID)
	if userID == "" {
		return fmt.Errorf("no local user for stripe customer %s", sub.Customer.ID)
	}
	saved, err := s.upsertSubscription(ctx, userID, sub, "")
	if err != nil {
		return err
	}
	if (sub.Status == stripe.SubscriptionStatusActive || sub.Status == stripe.SubscriptionStatusTrialing) &&
		saved.BillingStatus != "active" && s.OnSubscriptionActive != nil {
		return s.OnSubscriptionActive(ctx, userID)
	}
	if sub.Status == stripe.SubscriptionStatusPastDue && s.OnPaymentFailed != nil {
		return s.OnPaymentFailed(ctx, userID)
	}
	return nil
}

func (s *Service) lookupUserByCustomer(ctx context.Context, stripeCustomerID string) string {
	if stripeCustomerID == "" {
		return ""
	}
	c, err := s.repo.GetUserByStripeCustomer(ctx, stripeCustomerID)
	if err != nil {
		return ""
	}
	return c.UserID
}

func (s *Service) GetSubscription(ctx context.Context, userID string) (*models.Subscription, error) {
	sub, err := s.repo.GetSubscriptionByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return sub, nil
}

func (s *Service) ListInvoices(ctx context.Context, userID string) ([]models.Invoice, error) {
	return s.repo.ListInvoicesByUser(ctx, userID)
}

func unixTime(t int64) *time.Time {
	if t == 0 {
		return nil
	}
	tm := time.Unix(t, 0).UTC()
	return &tm
}

func jsonUnmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return errors.New("empty event payload")
	}
	return json.Unmarshal(data, v)
}

func customerUserID(ctx context.Context, s *Service, customerID string) string {
	return s.lookupUserByCustomer(ctx, customerID)
}
