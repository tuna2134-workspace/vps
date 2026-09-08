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

	// Stripe
	StripeSecretKey     string
	StripeWebhookSecret string

	// TLS for Control Plane <-> Agent gRPC (mTLS)
	TLSEnabled     bool
	TLSCertFile    string
	TLSKeyFile     string
	TLSCAFile      string
	ServerName     string
	// Node heartbeat offline threshold
	NodeOfflineAfter time.Duration
	// Node heartbeat degraded threshold
	NodeDegradedAfter time.Duration

	// Console
	ConsoleTokenTTL time.Duration
	// Public base URL used in console websocket URLs
	PublicBaseURL string
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

		GRPCClientTimeout: getenvDuration("GRPC_CLIENT_TIMEOUT", 30*time.Second),

		StripeSecretKey:     os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),

		TLSEnabled:        getenvBool("TLS_ENABLED", false),
		TLSCertFile:       getenv("TLS_CERT_FILE", ""),
		TLSKeyFile:        getenv("TLS_KEY_FILE", ""),
		TLSCAFile:         getenv("TLS_CA_FILE", ""),
		ServerName:        getenv("TLS_SERVER_NAME", "agent.internal"),
		NodeOfflineAfter:  getenvDuration("NODE_OFFLINE_AFTER", 60*time.Second),
		NodeDegradedAfter: getenvDuration("NODE_DEGRADED_AFTER", 30*time.Second),

		ConsoleTokenTTL: getenvDuration("CONSOLE_TOKEN_TTL", 2*time.Minute),
		PublicBaseURL:   getenv("PUBLIC_BASE_URL", "http://localhost:8080"),
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