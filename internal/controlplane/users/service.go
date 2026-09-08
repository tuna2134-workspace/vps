// Package users implements registration, authentication, password management,
// and session lifecycle. PII (address) is handled via a dedicated repository
// and never returned by API responses.
package users

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/auth"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
	"github.com/tuna2134/vps/internal/controlplane/session"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("email already registered")
	ErrAccountDisabled    = errors.New("account is disabled")
	ErrTooManyAttempts    = errors.New("too many failed login attempts")
	ErrInvalidToken       = errors.New("invalid session token")
	ErrSessionExpired     = errors.New("session expired")
	ErrSessionRevoked     = errors.New("session revoked")
)

var emailRegexp = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// RegistrationRequest is the set of required registration fields.
type RegistrationRequest struct {
	Email       string
	Password    string
	FirstName   string
	LastName    string
	LegalName   string
	Country     string
	PostalCode  string
	State       string
	City        string
	AddressLine1 string
	AddressLine2 string
}

func (r *RegistrationRequest) validate() error {
	if !emailRegexp.MatchString(r.Email) {
		return errors.New("invalid email address")
	}
	if len(r.Password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len(r.Password) > 256 {
		return errors.New("password too long")
	}
	if strings.TrimSpace(r.FirstName) == "" || strings.TrimSpace(r.LastName) == "" {
		return errors.New("first name and last name are required")
	}
	if strings.TrimSpace(r.LegalName) == "" {
		return errors.New("legal full name is required")
	}
	if strings.TrimSpace(r.Country) == "" || strings.TrimSpace(r.PostalCode) == "" ||
		strings.TrimSpace(r.State) == "" || strings.TrimSpace(r.City) == "" ||
		strings.TrimSpace(r.AddressLine1) == "" {
		return errors.New("complete billing address is required")
	}
	return nil
}

type Service struct {
	users      *repositories.UserRepository
	profiles   *repositories.UserProfileRepository
	sessions   *repositories.SessionRepository
	attempts   *repositories.LoginAttemptRepository
	audit      *audit.Service
	sessionTTL time.Duration
	tokenLen   int
	argon2     auth.Params
	lockout    lockoutConfig
}

type lockoutConfig struct {
	maxAttempts int
	window      time.Duration
}

type Options struct {
	SessionTTL         time.Duration
	SessionTokenLen    int
	Argon2             auth.Params
	LoginMaxAttempts   int
	LoginLockWindow    time.Duration
}

func NewService(
	users *repositories.UserRepository,
	profiles *repositories.UserProfileRepository,
	sessions *repositories.SessionRepository,
	attempts *repositories.LoginAttemptRepository,
	auditSvc *audit.Service,
	opts Options,
) *Service {
	return &Service{
		users:      users,
		profiles:   profiles,
		sessions:   sessions,
		attempts:   attempts,
		audit:      auditSvc,
		sessionTTL: opts.SessionTTL,
		tokenLen:   opts.SessionTokenLen,
		argon2:     opts.Argon2,
		lockout: lockoutConfig{
			maxAttempts: opts.LoginMaxAttempts,
			window:      opts.LoginLockWindow,
		},
	}
}

// Register creates a user account. The email is normalized to lowercase.
func (s *Service) Register(ctx context.Context, req RegistrationRequest) (*models.User, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.LegalName = strings.TrimSpace(req.LegalName)

	hash, err := auth.HashPassword(req.Password, s.argon2)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	u, err := s.users.Create(ctx, &models.User{
		Email:        req.Email,
		PasswordHash: hash,
		FirstName:    strings.TrimSpace(req.FirstName),
		LastName:     strings.TrimSpace(req.LastName),
		LegalName:    req.LegalName,
		Status:       models.UserStatusActive,
	})
	if err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}

	// Assign the default 'user' role.
	if err := s.users.AssignRole(ctx, u.ID, models.RoleUser); err != nil {
		return nil, err
	}

	// Store address PII in the isolated profile repository.
	if err := s.profiles.Upsert(ctx, &models.UserProfile{
		UserID:       u.ID,
		Country:      strings.TrimSpace(req.Country),
		PostalCode:   strings.TrimSpace(req.PostalCode),
		State:        strings.TrimSpace(req.State),
		City:         strings.TrimSpace(req.City),
		AddressLine1: strings.TrimSpace(req.AddressLine1),
		AddressLine2: strings.TrimSpace(req.AddressLine2),
	}); err != nil {
		return nil, err
	}

	_ = s.audit.Record(ctx, audit.Event{
		UserID:       u.ID,
		Action:       "user.register",
		ResourceType: "user",
		ResourceID:   u.ID,
	})
	return u, nil
}

// LoginResult carries the issued session and its raw token.
type LoginResult struct {
	Session *models.Session
	Token   string
	User    *models.User
}

// Login authenticates a user, applies rate limiting/lockout, and issues a
// session. Email enumeration is avoided by returning a generic error on all
// failure paths (after recording the attempt).
func (s *Service) Login(ctx context.Context, email, password, ip, userAgent string) (*LoginResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	if s.isLocked(ctx, email, ip) {
		_ = s.attempts.Record(ctx, email, ip, false)
		return nil, ErrTooManyAttempts
	}

	u, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		// Generic error; do not reveal whether the email exists.
		_ = s.attempts.Record(ctx, email, ip, false)
		return nil, ErrInvalidCredentials
	}

	ok, err := auth.VerifyPassword(password, u.PasswordHash)
	if err != nil {
		_ = s.attempts.Record(ctx, email, ip, false)
		return nil, ErrInvalidCredentials
	}
	if !ok {
		_ = s.attempts.Record(ctx, email, ip, false)
		return nil, ErrInvalidCredentials
	}

	if u.Status != models.UserStatusActive {
		_ = s.attempts.Record(ctx, email, ip, false)
		return nil, ErrAccountDisabled
	}

	_ = s.attempts.Record(ctx, email, ip, true)

	tok, err := session.NewToken(s.tokenLen)
	if err != nil {
		return nil, err
	}
	sess, err := s.sessions.Create(ctx, &models.Session{
		UserID:    u.ID,
		TokenHash: tok.Hash(),
		ExpiresAt: time.Now().Add(s.sessionTTL),
		IP:        ip,
		UserAgent: userAgent,
	})
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	_ = s.audit.Record(ctx, audit.Event{
		UserID:       u.ID,
		Action:       "user.login",
		ResourceType: "session",
		ResourceID:   sess.ID,
		IP:           ip,
		UserAgent:    userAgent,
	})
	return &LoginResult{Session: sess, Token: tok.String(), User: u}, nil
}

// Authenticate resolves a raw token into a valid session and user.
func (s *Service) Authenticate(ctx context.Context, rawToken string) (*models.Session, *models.User, error) {
	tok := session.TokenFromString(rawToken)
	sess, err := s.sessions.GetByTokenHash(ctx, tok.Hash())
	if err != nil {
		return nil, nil, ErrInvalidToken
	}
	if sess.RevokedAt != nil {
		return nil, nil, ErrSessionRevoked
	}
	if time.Now().After(sess.ExpiresAt) {
		return nil, nil, ErrSessionExpired
	}
	u, err := s.users.GetByID(ctx, sess.UserID)
	if err != nil {
		return nil, nil, ErrInvalidToken
	}
	if u.Status != models.UserStatusActive {
		return nil, nil, ErrAccountDisabled
	}
	return sess, u, nil
}

// TouchSession updates last_seen_at when stale.
func (s *Service) TouchSession(ctx context.Context, sess *models.Session, ip netip.Addr) {
	if repositories.ShouldTouch(sess.LastSeenAt) {
		_ = s.sessions.Touch(ctx, sess.ID, ip)
		sess.LastSeenAt = time.Now()
	}
}

// Logout revokes the given session.
func (s *Service) Logout(ctx context.Context, rawToken string) error {
	tok := session.TokenFromString(rawToken)
	sess, err := s.sessions.GetByTokenHash(ctx, tok.Hash())
	if err != nil {
		return ErrInvalidToken
	}
	if err := s.sessions.Revoke(ctx, sess.ID); err != nil {
		return err
	}
	_ = s.audit.Record(ctx, audit.Event{
		UserID:       sess.UserID,
		Action:       "session.logout",
		ResourceType: "session",
		ResourceID:   sess.ID,
	})
	return nil
}

// ListSessions returns the user's active sessions.
func (s *Service) ListSessions(ctx context.Context, userID string) ([]models.Session, error) {
	return s.sessions.ListByUser(ctx, userID, false)
}

// RevokeSession revokes a single session owned by the user.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	sess, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.UserID != userID {
		return repositories.ErrNotFound
	}
	if err := s.sessions.Revoke(ctx, sessionID); err != nil {
		return err
	}
	_ = s.audit.Record(ctx, audit.Event{
		UserID:       userID,
		Action:       "session.revoke",
		ResourceType: "session",
		ResourceID:   sessionID,
	})
	return nil
}

// RevokeAllSessions revokes every session for the user (e.g. on password change).
func (s *Service) RevokeAllSessions(ctx context.Context, userID, except string) error {
	_, err := s.sessions.RevokeByUser(ctx, userID, except)
	if err != nil {
		return err
	}
	_ = s.audit.Record(ctx, audit.Event{
		UserID:       userID,
		Action:       "session.revoke_all",
		ResourceType: "user",
		ResourceID:   userID,
	})
	return nil
}

// ChangePassword verifies the current password and updates to a new hash,
// revoking all other sessions.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	if len(newPassword) < 8 {
		return errors.New("new password must be at least 8 characters")
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	ok, err := auth.VerifyPassword(currentPassword, u.PasswordHash)
	if err != nil || !ok {
		return ErrInvalidCredentials
	}
	hash, err := auth.HashPassword(newPassword, s.argon2)
	if err != nil {
		return err
	}
	if err := s.users.UpdatePasswordHash(ctx, userID, hash); err != nil {
		return err
	}
	_ = s.audit.Record(ctx, audit.Event{
		UserID:       userID,
		Action:       "password.change",
		ResourceType: "user",
		ResourceID:   userID,
	})
	return nil
}

// GetRoles returns the roles for a user.
func (s *Service) GetRoles(ctx context.Context, userID string) ([]models.Role, error) {
	return s.users.GetRoles(ctx, userID)
}

// NewUUID returns a random UUID string.
func NewUUID() string {
	return uuid.NewString()
}

func (s *Service) isLocked(ctx context.Context, email, ip string) bool {
	byEmail, err := s.attempts.CountRecentFailures(ctx, email, s.lockout.window)
	if err == nil && byEmail >= s.lockout.maxAttempts {
		return true
	}
	byIP, err := s.attempts.CountRecentFailuresByIP(ctx, ip, s.lockout.window)
	if err == nil && byIP >= s.lockout.maxAttempts*3 {
		return true
	}
	return false
}