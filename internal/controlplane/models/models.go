package models

import (
	"time"
)

type UserStatus string

const (
	UserStatusPending   UserStatus = "pending"
	UserStatusActive    UserStatus = "active"
	UserStatusSuspended UserStatus = "suspended"
	UserStatusDisabled  UserStatus = "disabled"
)

type Role string

const (
	RoleUser    Role = "user"
	RoleSupport Role = "support"
	RoleAdmin   Role = "admin"
)

// User is the authentication identity. It deliberately does NOT contain the
// mailing address (PII); that lives in UserProfile.
type User struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`
	FirstName    string     `json:"first_name"`
	LastName     string     `json:"last_name"`
	LegalName    string     `json:"legal_name"`
	Status       UserStatus `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// UserProfile holds PII (legal name and address). It is handled by a
// dedicated repository and never included in API responses.
type UserProfile struct {
	UserID       string    `json:"user_id"`
	Country      string    `json:"country"`
	PostalCode   string    `json:"postal_code"`
	State        string    `json:"state"`
	City         string    `json:"city"`
	AddressLine1 string    `json:"address_line_1"`
	AddressLine2 string    `json:"address_line_2"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Session struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	TokenHash  string     `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	IP         string     `json:"ip"`
	UserAgent  string     `json:"user_agent"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type Cluster struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type NodeStatus string

const (
	NodeStatusHealthy  NodeStatus = "healthy"
	NodeStatusDegraded NodeStatus = "degraded"
	NodeStatusOffline  NodeStatus = "offline"
	NodeStatusDisabled NodeStatus = "disabled"
)

// NodeMetrics is the resource snapshot reported by an agent heartbeat.
type NodeMetrics struct {
	Hostname          string
	CPUCapacity       int
	MemoryTotalBytes  int64
	MemoryFreeBytes   int64
	StorageTotalBytes int64
	StorageFreeBytes  int64
	CPUUsagePercent   float64
	Healthy           bool
}

type Node struct {
	ID                 string     `json:"id"`
	ClusterID          string     `json:"cluster_id"`
	Name               string     `json:"name"`
	AgentEndpoint      string     `json:"agent_endpoint"`
	Status             NodeStatus `json:"status"`
	CPUCapacity        int        `json:"cpu_capacity"`
	MemoryCapacityMB   int64      `json:"memory_capacity_mb"`
	StorageCapacityGB  int64      `json:"storage_capacity_gb"`
	CPUUsagePercent    float64    `json:"cpu_usage_percent"`
	MemoryUsagePercent float64    `json:"memory_usage_percent"`
	LastHeartbeat      *time.Time `json:"last_heartbeat,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type StoragePool struct {
	ID         string `json:"id"`
	NodeID     string `json:"node_id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	TotalBytes int64  `json:"total_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
	Status     string `json:"status"`
}

type Plan struct {
	ID                string    `json:"id"`
	PlanID            string    `json:"-"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	VCPU              int       `json:"vcpu"`
	MemoryMB          int       `json:"memory_mb"`
	DiskGB            int       `json:"disk_gb"`
	BandwidthGB       int       `json:"bandwidth_gb"`
	NetworkSpeedMbps  int       `json:"network_speed_mbps"`
	IPv4Count         int       `json:"ipv4_count"`
	IPv6Prefix        int       `json:"ipv6_prefix"`
	MonthlyPriceCents int       `json:"monthly_price_cents"`
	Currency          string    `json:"currency"`
	Version           int       `json:"version"`
	Active            bool      `json:"active"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Network struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Bridge      string    `json:"bridge"`
	IPv4CIDR    string    `json:"ipv4_cidr"`
	IPv6CIDR    string    `json:"ipv6_cidr"`
	DNS1        string    `json:"dns1"`
	DNS2        string    `json:"dns2"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type IPPool struct {
	ID        string `json:"id"`
	NetworkID string `json:"network_id"`
	CIDR      string `json:"cidr"`
	Type      string `json:"type"`
	Gateway   string `json:"gateway"`
}

type IPAllocationStatus string

const (
	IPAllocationAllocated IPAllocationStatus = "allocated"
	IPAllocationReleased  IPAllocationStatus = "released"
)

type IPAllocation struct {
	ID          string             `json:"id"`
	PoolID      string             `json:"pool_id"`
	VMID        string             `json:"vm_id"`
	IPAddress   string             `json:"ip_address"`
	MACAddress  string             `json:"mac_address"`
	Gateway     string             `json:"gateway"`
	Prefix      int                `json:"prefix"`
	Status      IPAllocationStatus `json:"status"`
	AllocatedAt time.Time          `json:"allocated_at"`
	ReleasedAt  *time.Time         `json:"released_at,omitempty"`
}

type Image struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Version             string    `json:"version"`
	Architecture        string    `json:"architecture"`
	Format              string    `json:"format"`
	SourceURL           string    `json:"-"`
	Checksum            string    `json:"-"`
	SizeBytes           int64     `json:"size_bytes"`
	CloudInitCompatible bool      `json:"cloud_init_compatible"`
	KernelURL           string    `json:"-"`
	InitrdURL           string    `json:"-"`
	Cmdline             string    `json:"-"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type VMStatus string

const (
	VMStatusPending      VMStatus = "pending"
	VMStatusProvisioning VMStatus = "provisioning"
	VMStatusRunning      VMStatus = "running"
	VMStatusStopped      VMStatus = "stopped"
	VMStatusSuspended    VMStatus = "suspended"
	VMStatusTerminated   VMStatus = "terminated"
	VMStatusError        VMStatus = "error"
)

type VM struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	PlanID     string     `json:"plan_id,omitempty"`
	NodeID     string     `json:"node_id,omitempty"`
	NetworkID  string     `json:"network_id,omitempty"`
	ImageID    string     `json:"image_id,omitempty"`
	Name       string     `json:"name"`
	Hostname   string     `json:"hostname"`
	Status     VMStatus   `json:"status"`
	VCPU       int        `json:"vcpu"`
	MemoryMB   int        `json:"memory_mb"`
	DiskGB     int        `json:"disk_gb"`
	MACAddress string     `json:"mac_address"`
	InstanceID string     `json:"instance_id"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}

type OperationType string

const (
	OperationCreate    OperationType = "create"
	OperationDelete    OperationType = "delete"
	OperationStart     OperationType = "start"
	OperationStop      OperationType = "stop"
	OperationForceStop OperationType = "force_stop"
	OperationReboot    OperationType = "reboot"
	OperationTerminate OperationType = "terminate"
)

type OperationStatus string

const (
	OperationPending   OperationStatus = "pending"
	OperationRunning   OperationStatus = "running"
	OperationSucceeded OperationStatus = "succeeded"
	OperationFailed    OperationStatus = "failed"
)

type VMOperation struct {
	ID             string          `json:"id"`
	VMID           string          `json:"vm_id,omitempty"`
	OperationType  OperationType   `json:"operation_type"`
	Status         OperationStatus `json:"status"`
	IdempotencyKey string          `json:"-"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

type BillingCustomer struct {
	ID               string    `json:"id"`
	UserID           string    `json:"user_id"`
	StripeCustomerID string    `json:"stripe_customer_id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type Subscription struct {
	ID                   string     `json:"id"`
	UserID               string     `json:"user_id"`
	VMID                 string     `json:"vm_id,omitempty"`
	PlanID               string     `json:"plan_id,omitempty"`
	StripeSubscriptionID string     `json:"-"`
	Status               string     `json:"status"`
	BillingStatus        string     `json:"billing_status"`
	CurrentPeriodStart   *time.Time `json:"current_period_start,omitempty"`
	CurrentPeriodEnd     *time.Time `json:"current_period_end,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type Invoice struct {
	ID              string     `json:"id"`
	UserID          string     `json:"user_id"`
	StripeInvoiceID string     `json:"-"`
	AmountCents     int        `json:"amount_cents"`
	Currency        string     `json:"currency"`
	Status          string     `json:"status"`
	PaidAt          *time.Time `json:"paid_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type Payment struct {
	ID                    string    `json:"id"`
	UserID                string    `json:"user_id"`
	StripePaymentIntentID string    `json:"-"`
	AmountCents           int       `json:"amount_cents"`
	Currency              string    `json:"currency"`
	Status                string    `json:"status"`
	CreatedAt             time.Time `json:"created_at"`
}

type ConsoleToken struct {
	ID           string     `json:"id"`
	VMID         string     `json:"vm_id"`
	UserID       string     `json:"user_id"`
	TokenHash    string     `json:"-"`
	ConsoleType  string     `json:"console_type"`
	NodeEndpoint string     `json:"-"`
	VMName       string     `json:"-"`
	ExpiresAt    time.Time  `json:"expires_at"`
	UsedAt       *time.Time `json:"used_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type AuditLog struct {
	ID           string         `json:"id"`
	UserID       string         `json:"user_id,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	IP           string         `json:"ip,omitempty"`
	UserAgent    string         `json:"user_agent,omitempty"`
	Metadata     map[string]any `json:"metadata"`
	CreatedAt    time.Time      `json:"created_at"`
}
