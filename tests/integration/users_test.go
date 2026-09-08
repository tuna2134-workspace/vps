package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/auth"
	"github.com/tuna2134/vps/internal/controlplane/users"
)

// uniqueEmail returns a per-run unique email address.
func uniqueEmail(prefix string) string {
	return fmt.Sprintf("%s-%d@example.com", prefix, time.Now().UnixNano())
}

func newUserService(t *testing.T) *users.Service {
	t.Helper()
	auditSvc := audit.NewService(repos.Audit)
	return users.NewService(repos.Users, repos.UserProfiles, repos.Sessions, repos.LoginAttempts, auditSvc, users.Options{
		SessionTTL:       time.Hour,
		SessionTokenLen:  32,
		Argon2:           auth.DefaultParams,
		LoginMaxAttempts: 5,
		LoginLockWindow:  15 * time.Minute,
	})
}

// TestUserRegistrationAndLogin verifies the full register -> login -> session
// flow against PostgreSQL.
func TestUserRegistrationAndLogin(t *testing.T) {
	ctx := newContext(t)
	svc := newUserService(t)

	email := uniqueEmail("reg")
	u, err := svc.Register(ctx, users.RegistrationRequest{
		Email:        email,
		Password:     "password123",
		FirstName:    "Hanako",
		LastName:     "Sato",
		LegalName:    "Hanako Sato",
		Country:      "JP",
		PostalCode:   "150-0001",
		State:        "Tokyo",
		City:         "Shibuya",
		AddressLine1: "2-1 Dogenzaka",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if u.Email != email {
		t.Errorf("email mismatch: %s", u.Email)
	}

	// Duplicate registration must fail.
	if _, err := svc.Register(ctx, users.RegistrationRequest{
		Email: email, Password: "password123", FirstName: "a", LastName: "b",
		LegalName: "a b", Country: "JP", PostalCode: "1", State: "x", City: "y", AddressLine1: "z",
	}); err == nil {
		t.Error("expected duplicate registration to fail")
	}

	// Login with the right password.
	res, err := svc.Login(ctx, email, "password123", "127.0.0.1", "go-test")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if len(res.Token) < 32 {
		t.Errorf("session token too short: %d", len(res.Token))
	}

	// Wrong password must produce a generic error (no enumeration).
	if _, err := svc.Login(ctx, email, "wrong-password", "127.0.0.1", "go-test"); err == nil {
		t.Error("expected login to fail with wrong password")
	}
	// Unknown email must produce the SAME error.
	if _, err := svc.Login(ctx, "does-not-exist@example.com", "password123", "127.0.0.1", "go-test"); err == nil {
		t.Error("expected login to fail for unknown email")
	}

	// Authenticate with the session token.
	sess, authedUser, err := svc.Authenticate(ctx, res.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if authedUser.ID != u.ID {
		t.Errorf("authenticated user mismatch: %s != %s", authedUser.ID, u.ID)
	}
	if sess.UserID != u.ID {
		t.Errorf("session user mismatch")
	}

	// Logout revokes the session.
	if err := svc.Logout(ctx, res.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, _, err := svc.Authenticate(ctx, res.Token); err == nil {
		t.Error("expected authenticate to fail after logout")
	}
}

// TestPasswordHashing ensures Argon2id hashes are stored, never plaintext.
func TestPasswordHashing(t *testing.T) {
	ctx := newContext(t)
	svc := newUserService(t)
	u, err := svc.Register(ctx, users.RegistrationRequest{
		Email: uniqueEmail("hash"), Password: "s3cret!pass",
		FirstName: "a", LastName: "b", LegalName: "a b",
		Country: "JP", PostalCode: "1", State: "x", City: "y", AddressLine1: "z",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	stored, err := repos.Users.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if stored.PasswordHash == "s3cret!pass" {
		t.Error("password stored in plaintext!")
	}
	if len(stored.PasswordHash) == 0 || stored.PasswordHash[:1] != "$" {
		t.Errorf("expected argon2 hash format, got %q", stored.PasswordHash)
	}
}

// TestSessions test user ID string constants compile and sessions list.
func TestSessions(t *testing.T) {
	ctx := newContext(t)
	svc := newUserService(t)
	sessEmail := uniqueEmail("sess")
	u, err := svc.Register(ctx, users.RegistrationRequest{
		Email: sessEmail, Password: "password123",
		FirstName: "a", LastName: "b", LegalName: "a b",
		Country: "JP", PostalCode: "1", State: "x", City: "y", AddressLine1: "z",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// Create two sessions.
	_, err = svc.Login(ctx, sessEmail, "password123", "10.0.0.1", "ua1")
	if err != nil {
		t.Fatalf("login 1: %v", err)
	}
	_, err = svc.Login(ctx, sessEmail, "password123", "10.0.0.2", "ua2")
	if err != nil {
		t.Fatalf("login 2: %v", err)
	}

	sessions, err := svc.ListSessions(ctx, u.ID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}

	// Revoke all but one.
	if err := svc.RevokeAllSessions(ctx, u.ID, sessions[0].ID); err != nil {
		t.Fatalf("revoke all: %v", err)
	}
	remaining, err := svc.ListSessions(ctx, u.ID)
	if err != nil {
		t.Fatalf("list after revoke: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining session, got %d", len(remaining))
	}
}
