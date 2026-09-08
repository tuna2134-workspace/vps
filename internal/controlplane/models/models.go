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
	ID           string
	Email        string
	PasswordHash string
	FirstName    string
	LastName     string
	LegalName    string
	Status       UserStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// UserProfile holds PII (legal name and address). It is handled by a
// dedicated repository and never included in API responses.
type UserProfile struct {
	UserID       string
	Country      string
	PostalCode   string
	State        string
	City         string
	AddressLine1 string
	AddressLine2 string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	ID         string
	UserID     string
	TokenHash  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	IP         string
	UserAgent  string
	RevokedAt  *time.Time
}

type Cluster struct {
	ID          string
	Name        string
	Description string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
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
	ID                 string
	ClusterID          string
	Name               string
	AgentEndpoint      string
	Status             NodeStatus
	CPUCapacity        int
	MemoryCapacityMB   int64
	StorageCapacityGB  int64
	CPUUsagePercent    float64
	MemoryUsagePercent float64
	LastHeartbeat      *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type StoragePool struct {
	ID         string
	NodeID     string
	Name       string
	Path       string
	Type       string
	TotalBytes int64
	UsedBytes  int64
	Status     string
}

type Plan struct {
	ID                string
	PlanID            string
	Name              string
	Description       string
	VCPU              int
	MemoryMB          int
	DiskGB            int
	BandwidthGB       int
	NetworkSpeedMbps  int
	IPv4Count         int
	IPv6Prefix        int
	MonthlyPriceCents int
	Currency          string
	Version           int
	Active            bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Network struct {
	ID          string
	Name        string
	Description string
	Bridge      string
	IPv4CIDR    string
	IPv6CIDR    string
	DNS1        string
	DNS2        string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type IPPool struct {
	ID        string
	NetworkID string
	CIDR      string
	Type      string
	Gateway   string
}

type IPAllocationStatus string

const (
	IPAllocationAllocated IPAllocationStatus = "allocated"
	IPAllocationReleased  IPAllocationStatus = "released"
)

type IPAllocation struct {
	ID          string
	PoolID      string
	VMID        string
	IPAddress   string
	MACAddress  string
	Gateway     string
	Prefix      int
	Status      IPAllocationStatus
	AllocatedAt time.Time
	ReleasedAt  *time.Time
}

type Image struct {
	ID                  string
	Name                string
	Version             string
	Architecture        string
	Format              string
	SourceURL           string
	Checksum            string
	SizeBytes           int64
	CloudInitCompatible bool
	Status              string
	CreatedAt           time.Time
	UpdatedAt           time.Time
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
	ID         string
	UserID     string
	PlanID     string
	NodeID     string
	NetworkID  string
	ImageID    string
	Name       string
	Hostname   string
	Status     VMStatus
	VCPU       int
	MemoryMB   int
	DiskGB     int
	MACAddress string
	InstanceID string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
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
	ID             string
	VMID           string
	OperationType  OperationType
	Status         OperationStatus
	IdempotencyKey string
	Error          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
}

type BillingCustomer struct {
	ID               string
	UserID           string
	StripeCustomerID string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Subscription struct {
	ID                   string
	UserID               string
	VMID                 string
	PlanID               string
	StripeSubscriptionID string
	Status               string
	BillingStatus        string
	CurrentPeriodStart   *time.Time
	CurrentPeriodEnd     *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type Invoice struct {
	ID              string
	UserID          string
	StripeInvoiceID string
	AmountCents     int
	Currency        string
	Status          string
	PaidAt          *time.Time
	CreatedAt       time.Time
}

type Payment struct {
	ID                    string
	UserID                string
	StripePaymentIntentID string
	AmountCents           int
	Currency              string
	Status                string
	CreatedAt             time.Time
}

type ConsoleToken struct {
	ID          string
	VMID        string
	UserID      string
	TokenHash   string
	ConsoleType string
	Host        string
	Port        int
	ExpiresAt   time.Time
	UsedAt      *time.Time
	CreatedAt   time.Time
}

type AuditLog struct {
	ID           string
	UserID       string
	Action       string
	ResourceType string
	ResourceID   string
	IP           string
	UserAgent    string
	Metadata     map[string]any
	CreatedAt    time.Time
}
