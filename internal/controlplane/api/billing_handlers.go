package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/tuna2134/vps/internal/controlplane/billing"
)

type checkoutRequest struct {
	StripePriceID string `json:"stripe_price_id"`
}

type subscriptionResponse struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	BillingStatus    string `json:"billing_status"`
	CurrentPeriodEnd string `json:"current_period_end"`
}

type BillingHandlers struct {
	billing *billing.Service
}

func NewBillingHandlers(billingSvc *billing.Service) *BillingHandlers {
	return &BillingHandlers{billing: billingSvc}
}

func (h *BillingHandlers) GetSubscription(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	sub, err := h.billing.GetSubscription(r.Context(), u.ID)
	if err != nil {
		if errors.Is(err, billing.ErrNotFound) {
			writeData(w, http.StatusOK, nil)
			return
		}
		mapError(w, err)
		return
	}
	periodEnd := ""
	if sub.CurrentPeriodEnd != nil {
		periodEnd = sub.CurrentPeriodEnd.Format("2006-01-02T15:04:05Z")
	}
	writeData(w, http.StatusOK, subscriptionResponse{
		ID:               sub.ID,
		Status:           sub.Status,
		BillingStatus:    sub.BillingStatus,
		CurrentPeriodEnd: periodEnd,
	})
}

func (h *BillingHandlers) ListInvoices(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	invoices, err := h.billing.ListInvoices(r.Context(), u.ID)
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, invoices)
}

func (h *BillingHandlers) CreateSubscription(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	var req checkoutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.StripePriceID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "stripe_price_id is required")
		return
	}
	customer, err := h.billing.GetOrCreateCustomer(r.Context(), u.ID, u.Email)
	if err != nil {
		mapError(w, err)
		return
	}
	sub, err := h.billing.CreateSubscription(r.Context(), u.ID, customer.StripeCustomerID, req.StripePriceID)
	if err != nil {
		mapError(w, err)
		return
	}
	periodEnd := ""
	if sub.CurrentPeriodEnd != nil {
		periodEnd = sub.CurrentPeriodEnd.Format("2006-01-02T15:04:05Z")
	}
	writeData(w, http.StatusCreated, subscriptionResponse{
		ID:               sub.ID,
		Status:           sub.Status,
		BillingStatus:    sub.BillingStatus,
		CurrentPeriodEnd: periodEnd,
	})
}

// StripeWebhook receives and verifies Stripe webhook events. Processing is
// quick and idempotent; long-running side effects are delegated to callbacks.
func (h *BillingHandlers) StripeWebhook(w http.ResponseWriter, r *http.Request) {
	const maxBody = 1 << 20 // 1 MiB
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "unable to read request body")
		return
	}
	processed, err := h.billing.HandleWebhook(r.Context(), body, r.Header.Get("Stripe-Signature"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "WEBHOOK_ERROR", "unable to verify or process webhook")
		return
	}
	if !processed {
		// Duplicate event; acknowledge to avoid Stripe retries.
		writeData(w, http.StatusOK, map[string]any{"already_processed": true})
		return
	}
	writeData(w, http.StatusOK, map[string]any{"received": true})
}
