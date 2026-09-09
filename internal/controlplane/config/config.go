package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the Control Plane.
// It is populated from environment variables with sane development defaults.
type Config struct {
	Env   string
	Debug bool

	HTTPListenAddress string
	GRPCListenAddress string

	// HTTP TLS (HTTPS). When enabled the HTTP server serves TLS with the
	// given certificate/key.
	HTTPTLS         bool
	HTTPTLSCertFile string
	HTTPTLSKeyFile  string

	DatabaseURL string

	// Session / auth
	SessionTTL       time.Duration
	SessionTokenLen  int
	LoginMaxAttempts int
	LoginLockWindow  time.Duration
	PasswordResetTTL time.Duration

	// Argon2id parameters
	Argon2Memory      uint32
	Argon2Iterations  uint32
	Argon2Parallelism uint8
	Argon2SaltLength  uint32
	Argon2KeyLength   uint32

	// gRPC -> Agent
	GRPCClientTimeout time.Duration
	// Agent RPC retry policy for transient failures.
	AgentRPCMaxRetries     int
	AgentRPCInitialBackoff time.Duration
	AgentRPCMaxBackoff     time.Duration

	// Stripe
	StripeSecretKey     string
	StripeWebhookSecret string

	// Billing suspension / grace period
	BillingGracePeriod   time.Duration
	BillingSweepInterval time.Duration

	// TLS for Control Plane <-> Agent gRPC (mTLS)
	TLSEnabled  bool
	TLSCertFile string
	TLSKeyFile  string
	TLSCAFile   string
	ServerName  string
	// AgentAuthToken is the shared secret sent to agents as a Bearer token.
	// It protects agent gRPC even when TLS is disabled.
	AgentAuthToken string
	// Node heartbeat offline threshold
	NodeOfflineAfter time.Duration
	// Node heartbeat degraded threshold
	NodeDegradedAfter time.Duration
	// VM/libvirt state reconciliation interval
	ReconcileInterval time.Duration

	// Console
	ConsoleTokenTTL time.Duration
	// Public base URL used in console websocket URLs
	PublicBaseURL string

	// TrustedProxyCIDRs lists reverse-proxy networks whose forwarded headers
	// (X-Forwarded-For) are trusted when resolving the client IP.
	TrustedProxyCIDRs []string
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		i, err := strconv.Atoi(v)
		if err == nil {
			return i
		}
	}
	return def
}

func getenvUint32(key string, def uint32) uint32 {
	if v := os.Getenv(key); v != "" {
		u, err := strconv.ParseUint(v, 10, 32)
		if err == nil {
			return uint32(u)
		}
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return def
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	cfg := &Config{
		Env:               getenv("ENV", "development"),
		Debug:             getenvBool("DEBUG", false),
		HTTPListenAddress: getenv("HTTP_LISTEN_ADDRESS", ":8080"),
		GRPCListenAddress: getenv("GRPC_LISTEN_ADDRESS", ":9000"),
		DatabaseURL:       getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/vps?sslmode=disable"),
		HTTPTLS:           getenvBool("HTTP_TLS_ENABLED", false),
		HTTPTLSCertFile:   getenv("HTTP_TLS_CERT_FILE", ""),
		HTTPTLSKeyFile:    getenv("HTTP_TLS_KEY_FILE", ""),

		SessionTTL:       getenvDuration("SESSION_TTL", 24*time.Hour),
		SessionTokenLen:  getenvInt("SESSION_TOKEN_LEN", 32),
		LoginMaxAttempts: getenvInt("LOGIN_MAX_ATTEMPTS", 5),
		LoginLockWindow:  getenvDuration("LOGIN_LOCK_WINDOW", 15*time.Minute),
		PasswordResetTTL: getenvDuration("PASSWORD_RESET_TTL", 30*time.Minute),

		Argon2Memory:      getenvUint32("ARGON2_MEMORY", 64*1024),
		Argon2Iterations:  getenvUint32("ARGON2_ITERATIONS", 3),
		Argon2Parallelism: uint8(getenvInt("ARGON2_PARALLELISM", 2)),
		Argon2SaltLength:  getenvUint32("ARGON2_SALT_LENGTH", 16),
		Argon2KeyLength:   getenvUint32("ARGON2_KEY_LENGTH", 32),

		GRPCClientTimeout:      getenvDuration("GRPC_CLIENT_TIMEOUT", 30*time.Second),
		AgentRPCMaxRetries:     getenvInt("AGENT_RPC_MAX_RETRIES", 5),
		AgentRPCInitialBackoff: getenvDuration("AGENT_RPC_INITIAL_BACKOFF", 500*time.Millisecond),
		AgentRPCMaxBackoff:     getenvDuration("AGENT_RPC_MAX_BACKOFF", 5*time.Second),

		StripeSecretKey:     os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),

		BillingGracePeriod:   getenvDuration("BILLING_GRACE_PERIOD", 7*24*time.Hour),
		BillingSweepInterval: getenvDuration("BILLING_SWEEP_INTERVAL", time.Hour),

		TLSEnabled:        getenvBool("TLS_ENABLED", false),
		TLSCertFile:       getenv("TLS_CERT_FILE", ""),
		TLSKeyFile:        getenv("TLS_KEY_FILE", ""),
		TLSCAFile:         getenv("TLS_CA_FILE", ""),
		ServerName:        getenv("TLS_SERVER_NAME", "agent.internal"),
		AgentAuthToken:    getenv("AGENT_AUTH_TOKEN", ""),
		NodeOfflineAfter:  getenvDuration("NODE_OFFLINE_AFTER", 60*time.Second),
		NodeDegradedAfter: getenvDuration("NODE_DEGRADED_AFTER", 30*time.Second),
		ReconcileInterval: getenvDuration("RECONCILE_INTERVAL", 45*time.Second),

		ConsoleTokenTTL: getenvDuration("CONSOLE_TOKEN_TTL", 2*time.Minute),
		PublicBaseURL:   getenv("PUBLIC_BASE_URL", "http://localhost:8080"),
		TrustedProxyCIDRs: splitCSV(getenv("TRUSTED_PROXY_CIDRS",
			"127.0.0.1/32, ::1/128")),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.SessionTokenLen < 16 {
		return fmt.Errorf("SESSION_TOKEN_LEN must be at least 16 bytes")
	}
	if c.Argon2Memory < 64*1024 {
		return fmt.Errorf("ARGON2_MEMORY is too low")
	}
	if c.HTTPTLS {
		if c.HTTPTLSCertFile == "" || c.HTTPTLSKeyFile == "" {
			return fmt.Errorf("HTTP_TLS_ENABLED=true requires HTTP_TLS_CERT_FILE and HTTP_TLS_KEY_FILE")
		}
	}
	return nil
}

// DatabaseName extracts the database name from the DSN (used by tests/tooling).
func DatabaseName(dsn string) string {
	for _, part := range strings.Split(dsn, " ") {
		if strings.HasPrefix(part, "dbname=") {
			return strings.TrimPrefix(part, "dbname=")
		}
	}
	return "vps"
}

// splitCSV splits a comma-separated list, trimming spaces and empty entries.
func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
