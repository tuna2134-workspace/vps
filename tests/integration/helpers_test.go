package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/users"
)

// testLogger returns a quiet logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestUser registers a throwaway user and returns it.
func newTestUser(t *testing.T, ctx context.Context) *models.User {
	t.Helper()
	svc := newUserService(t)
	u, err := svc.Register(ctx, users.RegistrationRequest{
		Email: uniqueEmail("user"), Password: "password123",
		FirstName: "a", LastName: "b", LegalName: "a b",
		Country: "JP", PostalCode: "1", State: "x", City: "y", AddressLine1: "z",
	})
	if err != nil {
		t.Fatalf("register test user: %v", err)
	}
	return u
}

// uniqueMAC returns a per-run unique locally-administered MAC.
func uniqueMAC() string {
	n := time.Now().UnixNano()
	return fmt.Sprintf("02:00:00:%02x:%02x:%02x", byte(n>>16), byte(n>>8), byte(n))
}

// newTestVM creates a VM record owned by the given user.
func newTestVM(t *testing.T, ctx context.Context, userID string) *models.VM {
	t.Helper()
	suffix := time.Now().UnixNano()
	vm, err := repos.VMs.Create(ctx, &models.VM{
		UserID:     userID,
		Name:       fmt.Sprintf("vm-%d", suffix),
		Hostname:   fmt.Sprintf("vm-%d", suffix),
		Status:     models.VMStatusPending,
		VCPU:       1,
		MemoryMB:   1024,
		DiskGB:     10,
		MACAddress: fmt.Sprintf("02:00:00:00:%02x:%02x", byte(suffix>>8), byte(suffix)),
		InstanceID: fmt.Sprintf("i-%d", suffix),
	})
	if err != nil {
		t.Fatalf("create test vm: %v", err)
	}
	return vm
}
