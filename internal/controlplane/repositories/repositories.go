package repositories

import "github.com/jackc/pgx/v5/pgxpool"

// Repositories aggregates all repository implementations.
type Repositories struct {
	Users           *UserRepository
	UserProfiles    *UserProfileRepository
	Sessions        *SessionRepository
	LoginAttempts   *LoginAttemptRepository
	Clusters        *ClusterRepository
	Nodes           *NodeRepository
	StoragePools    *StoragePoolRepository
	Plans           *PlanRepository
	Networks        *NetworkRepository
	IPPools         *IPPoolRepository
	Images          *ImageRepository
	VMs             *VMRepository
	Operations      *OperationRepository
	Billing         *BillingRepository
	ConsoleTokens   *ConsoleTokenRepository
	Audit           *AuditRepository
}

// New builds all repositories backed by the given connection pool.
func New(pool *pgxpool.Pool) *Repositories {
	return &Repositories{
		Users:         NewUserRepository(pool),
		UserProfiles:  NewUserProfileRepository(pool),
		Sessions:      NewSessionRepository(pool),
		LoginAttempts: NewLoginAttemptRepository(pool),
		Clusters:      NewClusterRepository(pool),
		Nodes:         NewNodeRepository(pool),
		StoragePools:  NewStoragePoolRepository(pool),
		Plans:         NewPlanRepository(pool),
		Networks:      NewNetworkRepository(pool),
		IPPools:       NewIPPoolRepository(pool),
		Images:        NewImageRepository(pool),
		VMs:           NewVMRepository(pool),
		Operations:    NewOperationRepository(pool),
		Billing:       NewBillingRepository(pool),
		ConsoleTokens: NewConsoleTokenRepository(pool),
		Audit:         NewAuditRepository(pool),
	}
}