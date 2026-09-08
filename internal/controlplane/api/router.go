package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tuna2134/vps/internal/controlplane/billing"
	"github.com/tuna2134/vps/internal/controlplane/cluster"
	"github.com/tuna2134/vps/internal/controlplane/console"
	"github.com/tuna2134/vps/internal/controlplane/images"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/networks"
	"github.com/tuna2134/vps/internal/controlplane/plans"
	"github.com/tuna2134/vps/internal/controlplane/users"
	"github.com/tuna2134/vps/internal/controlplane/vms"
)

// Dependencies aggregates the services the router needs.
type Dependencies struct {
	Users      *users.Service
	VMs        *vms.Service
	Plans      *plans.Service
	Networks   *networks.Service
	Clusters   *cluster.Service
	Images     *images.Service
	Billing    *billing.Service
	Console    *console.Service
	ConsoleWS  http.HandlerFunc
	ReadyCheck func(ctx context.Context) error
}

// NewRouter wires all routes and middleware.
func NewRouter(deps Dependencies, log *slog.Logger) http.Handler {
	r := chi.NewRouter()

	// RealIP, recovery and request logging apply to every route including the
	// long-lived console WebSocket.
	r.Use(middleware.RealIP)
	r.Use(Recoverer(log))
	r.Use(RequestLogger(log))

	authMW := NewAuthMiddleware(deps.Users, log)
	authHandlers := NewAuthHandlers(deps.Users)
	vmHandlers := NewVMHandlers(deps.VMs)
	planHandlers := NewPlanHandlers(deps.Plans)
	networkHandlers := NewNetworkHandlers(deps.Networks)
	clusterHandlers := NewClusterHandlers(deps.Clusters)
	imageHandlers := NewImageHandlers(deps.Images)
	billingHandlers := NewBillingHandlers(deps.Billing)
	consoleHandlers := NewConsoleHandlers(deps.Console, deps.VMs)
	health := &Health{Ready: deps.ReadyCheck}

	// Health / readiness
	r.Get("/healthz", health.Liveness)
	r.Get("/readyz", health.Readiness)

	// Console websocket gateway (public; one-time token required). Registered
	// in its own group WITHOUT the request timeout so long-lived sessions are
	// not cut short.
	if deps.ConsoleWS != nil {
		r.Group(func(ws chi.Router) {
			ws.Get("/console/ws", deps.ConsoleWS)
		})
	}

	// All regular API routes are subject to the request timeout.
	r.Group(func(api chi.Router) {
		api.Use(middleware.Timeout(60 * time.Second))

		// Auth (public)
		api.Post("/v1/auth/register", authHandlers.Register)
		api.Post("/v1/auth/login", authHandlers.Login)

		// Public catalog
		api.Get("/v1/plans", planHandlers.List)
		api.Get("/v1/plans/{id}", planHandlers.Get)

		// Webhooks (public, signature verified inside)
		api.Post("/v1/webhooks/stripe", billingHandlers.StripeWebhook)

		// Authenticated routes
		api.Group(func(priv chi.Router) {
			priv.Use(authMW.Authenticate)

			priv.Post("/v1/auth/logout", authHandlers.Logout)
			priv.Get("/v1/auth/sessions", authHandlers.ListSessions)
			priv.Delete("/v1/auth/sessions/{id}", authHandlers.RevokeSession)
			priv.Post("/v1/auth/sessions/revoke_all", authHandlers.RevokeAllSessions)
			priv.Post("/v1/auth/change-password", authHandlers.ChangePassword)

			priv.Get("/v1/users/me", authHandlers.Me)

			priv.Get("/v1/vms", vmHandlers.ListVMs)
			priv.Post("/v1/vms", vmHandlers.CreateVM)
			priv.Get("/v1/vms/{id}", vmHandlers.GetVM)
			priv.Post("/v1/vms/{id}/start", vmHandlers.VMOperation)
			priv.Post("/v1/vms/{id}/stop", vmHandlers.VMOperation)
			priv.Post("/v1/vms/{id}/force_stop", vmHandlers.VMOperation)
			priv.Post("/v1/vms/{id}/reboot", vmHandlers.VMOperation)
			priv.Delete("/v1/vms/{id}", vmHandlers.DeleteVM)
			priv.Post("/v1/vms/{id}/console", consoleHandlers.IssueVMConsole)

			priv.Get("/v1/operations/{id}", vmHandlers.GetOperation)

			priv.Get("/v1/billing/subscription", billingHandlers.GetSubscription)
			priv.Get("/v1/billing/invoices", billingHandlers.ListInvoices)
			priv.Post("/v1/billing/subscription", billingHandlers.CreateSubscription)

			priv.Get("/v1/images", imageHandlers.List)
			priv.Get("/v1/images/{id}", imageHandlers.Get)

			priv.Get("/v1/networks", networkHandlers.List)
			priv.Get("/v1/networks/{id}", networkHandlers.Get)
			priv.Get("/v1/networks/{id}/pools", networkHandlers.ListPools)
		})

		// Admin / support routes
		api.Group(func(admin chi.Router) {
			admin.Use(authMW.Authenticate)
			admin.Use(RequireRole(models.RoleSupport))

			admin.Post("/v1/images", imageHandlers.Create)

			admin.Post("/v1/networks", networkHandlers.Create)
			admin.Post("/v1/networks/{id}/pools", networkHandlers.AddPool)
			admin.Patch("/v1/networks/{id}/status", networkHandlers.SetStatus)

			admin.Get("/v1/clusters", clusterHandlers.ListClusters)
			admin.Post("/v1/clusters", clusterHandlers.CreateCluster)
			admin.Get("/v1/nodes", clusterHandlers.ListNodes)
			admin.Post("/v1/nodes", clusterHandlers.RegisterNode)
			admin.Get("/v1/nodes/{id}", clusterHandlers.GetNode)
			admin.Get("/v1/nodes/storage-pools", clusterHandlers.ListStoragePools)

			admin.Post("/v1/plans", planHandlers.Create)
			admin.Put("/v1/plans/{id}", planHandlers.Update)
			admin.Patch("/v1/plans/{id}/active", planHandlers.SetActive)
			admin.Get("/v1/plans/{id}/versions", planHandlers.ListVersions)
		})
	})

	return r
}
