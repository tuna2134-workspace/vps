package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

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

func (h *BillingHandlers) GetSubscription(c *gin.Context) {
	u := UserFrom(c)
	sub, err := h.billing.GetSubscription(c.Request.Context(), u.ID)
	if err != nil {
		if errors.Is(err, billing.ErrNotFound) {
			writeData(c, http.StatusOK, nil)
			return
		}
		mapError(c, err)
		return
	}
	periodEnd := ""
	if sub.CurrentPeriodEnd != nil {
		periodEnd = sub.CurrentPeriodEnd.Format("2006-01-02T15:04:05Z")
	}
	writeData(c, http.StatusOK, subscriptionResponse{
		ID:               sub.ID,
		Status:           sub.Status,
		BillingStatus:    sub.BillingStatus,
		CurrentPeriodEnd: periodEnd,
	})
}

func (h *BillingHandlers) ListInvoices(c *gin.Context) {
	u := UserFrom(c)
	invoices, err := h.billing.ListInvoices(c.Request.Context(), u.ID)
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, invoices)
}

func (h *BillingHandlers) CreateSubscription(c *gin.Context) {
	u := UserFrom(c)
	var req checkoutRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.StripePriceID == "" {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "stripe_price_id is required")
		return
	}
	customer, err := h.billing.GetOrCreateCustomer(c.Request.Context(), u.ID, u.Email)
	if err != nil {
		mapError(c, err)
		return
	}
	sub, err := h.billing.CreateSubscription(c.Request.Context(), u.ID, customer.StripeCustomerID, req.StripePriceID)
	if err != nil {
		mapError(c, err)
		return
	}
	periodEnd := ""
	if sub.CurrentPeriodEnd != nil {
		periodEnd = sub.CurrentPeriodEnd.Format("2006-01-02T15:04:05Z")
	}
	writeData(c, http.StatusCreated, subscriptionResponse{
		ID:               sub.ID,
		Status:           sub.Status,
		BillingStatus:    sub.BillingStatus,
		CurrentPeriodEnd: periodEnd,
	})
}

// StripeWebhook receives and verifies Stripe webhook events. Processing is
// quick and idempotent; long-running side effects are delegated to callbacks.
func (h *BillingHandlers) StripeWebhook(c *gin.Context) {
	const maxBody = 1 << 20 // 1 MiB
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxBody))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "unable to read request body")
		return
	}
	processed, err := h.billing.HandleWebhook(c.Request.Context(), body, c.Request.Header.Get("Stripe-Signature"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "WEBHOOK_ERROR", "unable to verify or process webhook")
		return
	}
	if !processed {
		// Duplicate event; acknowledge to avoid Stripe retries.
		writeData(c, http.StatusOK, map[string]any{"already_processed": true})
		return
	}
	writeData(c, http.StatusOK, map[string]any{"received": true})
}
