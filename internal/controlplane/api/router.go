package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tuna2134/vps/internal/controlplane/billing"
	"github.com/tuna2134/vps/internal/controlplane/cluster"
	"github.com/tuna2134/vps/internal/controlplane/console"
	"github.com/tuna2134/vps/internal/controlplane/images"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/networks"
	"github.com/tuna2134/vps/internal/controlplane/plans"
	"github.com/tuna2134/vps/internal/controlplane/proxytrust"
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
	// ProxyTrust resolves the client IP, honoring forwarded headers only for
	// trusted proxies. When nil, forwarded headers are ignored entirely.
	ProxyTrust *proxytrust.ProxyTrust
}

// NewRouter wires all routes and middleware and returns an http.Handler.
func NewRouter(deps Dependencies, log *slog.Logger) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// Disable gin's default writer to keep the stdout clean; our logger covers
	// access logging.
	r.SetTrustedProxies(nil)

	// Recovery and structured request logging apply to every route including
	// the long-lived console WebSocket.
	trust := deps.ProxyTrust
	if trust == nil {
		trust, _ = proxytrust.New(nil) // fail closed: trust no forwarded headers
	}
	r.Use(Recoverer(log))
	r.Use(RequestLogger(log, trust))

	authMW := NewAuthMiddleware(deps.Users, trust, log)
	authHandlers := NewAuthHandlers(deps.Users, trust)
	vmHandlers := NewVMHandlers(deps.VMs)
	planHandlers := NewPlanHandlers(deps.Plans)
	networkHandlers := NewNetworkHandlers(deps.Networks)
	clusterHandlers := NewClusterHandlers(deps.Clusters)
	imageHandlers := NewImageHandlers(deps.Images)
	billingHandlers := NewBillingHandlers(deps.Billing)
	consoleHandlers := NewConsoleHandlers(deps.Console, deps.VMs)
	health := &Health{Ready: deps.ReadyCheck}

	// Health / readiness
	r.GET("/healthz", health.Liveness)
	r.GET("/readyz", health.Readiness)

	// Console websocket gateway (public; one-time token required). Registered
	// in its own route WITHOUT the request timeout so long-lived sessions are
	// not cut short.
	if deps.ConsoleWS != nil {
		r.GET("/console/ws", gin.WrapH(deps.ConsoleWS))
	}

	// All regular API routes are subject to the request timeout.
	api := r.Group("/v1")
	api.Use(Timeout(60 * time.Second))

	// Auth (public)
	api.POST("/auth/register", authHandlers.Register)
	api.POST("/auth/login", authHandlers.Login)

	// Public catalog
	api.GET("/plans", planHandlers.List)
	api.GET("/plans/:id", planHandlers.Get)

	// Webhooks (public, signature verified inside)
	api.POST("/webhooks/stripe", billingHandlers.StripeWebhook)

	// Authenticated routes
	priv := api.Group("")
	priv.Use(authMW.Authenticate)

	priv.POST("/auth/logout", authHandlers.Logout)
	priv.GET("/auth/sessions", authHandlers.ListSessions)
	priv.DELETE("/auth/sessions/:id", authHandlers.RevokeSession)
	priv.POST("/auth/sessions/revoke_all", authHandlers.RevokeAllSessions)
	priv.POST("/auth/change-password", authHandlers.ChangePassword)

	priv.GET("/users/me", authHandlers.Me)

	priv.GET("/vms", vmHandlers.ListVMs)
	priv.POST("/vms", vmHandlers.CreateVM)
	priv.GET("/vms/:id", vmHandlers.GetVM)
	priv.POST("/vms/:id/:action", vmHandlers.VMOperation)
	priv.DELETE("/vms/:id", vmHandlers.DeleteVM)
	priv.POST("/vms/:id/console", consoleHandlers.IssueVMConsole)

	priv.GET("/operations/:id", vmHandlers.GetOperation)

	priv.GET("/billing/subscription", billingHandlers.GetSubscription)
	priv.GET("/billing/invoices", billingHandlers.ListInvoices)
	priv.POST("/billing/subscription", billingHandlers.CreateSubscription)

	priv.GET("/images", imageHandlers.List)
	priv.GET("/images/:id", imageHandlers.Get)

	priv.GET("/networks", networkHandlers.List)
	priv.GET("/networks/:id", networkHandlers.Get)
	priv.GET("/networks/:id/pools", networkHandlers.ListPools)

	// Admin / support routes
	admin := api.Group("")
	admin.Use(authMW.Authenticate)
	admin.Use(RequireRole(models.RoleSupport))

	admin.POST("/images", imageHandlers.Create)

	admin.POST("/networks", networkHandlers.Create)
	admin.POST("/networks/:id/pools", networkHandlers.AddPool)
	admin.PATCH("/networks/:id/status", networkHandlers.SetStatus)

	admin.GET("/clusters", clusterHandlers.ListClusters)
	admin.POST("/clusters", clusterHandlers.CreateCluster)
	admin.GET("/nodes", clusterHandlers.ListNodes)
	admin.POST("/nodes", clusterHandlers.RegisterNode)
	admin.GET("/nodes/:id", clusterHandlers.GetNode)
	admin.GET("/nodes/storage-pools", clusterHandlers.ListStoragePools)

	admin.POST("/plans", planHandlers.Create)
	admin.PUT("/plans/:id", planHandlers.Update)
	admin.PATCH("/plans/:id/active", planHandlers.SetActive)
	admin.GET("/plans/:id/versions", planHandlers.ListVersions)

	return r
}
