package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds agent runtime configuration.
type Config struct {
	// Agent identity used in heartbeats and node registration.
	AgentID string
	// gRPC listen address for the AgentService.
	GRPCListenAddress string
	// URI used to connect to the local libvirt daemon.
	LibvirtURI string
	// Default storage pool used for VM disks.
	StoragePool string
	// Directory for transient files (downloaded images, cloud-init ISOs).
	WorkDir string
	// Graceful shutdown timeout.
	ShutdownTimeout time.Duration

	// TLS (mTLS). When enabled, the agent presents its certificate and
	// requires the Control Plane to present a certificate signed by the CA.
	TLS        bool
	CertFile   string
	KeyFile    string
	CAFile     string

	// Heartbeat interval used when the control plane drives heartbeats.
	HeartbeatInterval time.Duration

	// Whether this is a test/development agent with libvirt disabled.
	FakeMode bool
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

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return def
}

// Load reads the agent configuration from the environment.
func Load() *Config {
	return &Config{
		AgentID:           getenv("AGENT_ID", "agent-1"),
		GRPCListenAddress: getenv("AGENT_GRPC_LISTEN_ADDRESS", ":9001"),
		LibvirtURI:        getenv("LIBVIRT_URI", "qemu:///system"),
		StoragePool:       getenv("AGENT_STORAGE_POOL", "default"),
		WorkDir:           getenv("AGENT_WORK_DIR", "/var/lib/vps-agent"),
		ShutdownTimeout:   getenvDuration("AGENT_SHUTDOWN_TIMEOUT", 15*time.Second),
		TLS:               getenvBool("AGENT_TLS_ENABLED", false),
		CertFile:          getenv("AGENT_TLS_CERT_FILE", ""),
		KeyFile:           getenv("AGENT_TLS_KEY_FILE", ""),
		CAFile:            getenv("AGENT_TLS_CA_FILE", ""),
		HeartbeatInterval: getenvDuration("AGENT_HEARTBEAT_INTERVAL", 15*time.Second),
		FakeMode:          getenvBool("AGENT_FAKE_MODE", false),
	}
}