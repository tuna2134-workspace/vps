// Package auth implements password hashing with Argon2id and login
// rate-limiting/lockout logic following the OWASP Authentication Cheat Sheet.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalidHash = errors.New("invalid password hash format")
	ErrMismatch    = errors.New("password does not match")
)

// Params holds Argon2id parameters.
type Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams are OWASP-recommended Argon2id parameters.
var DefaultParams = Params{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

type hashedPassword struct {
	Params
	Salt []byte
	Hash []byte
}

// HashPassword derives an Argon2id hash from the given password.
func HashPassword(password string, p Params) (string, error) {
	if len(password) == 0 {
		return "", errors.New("password must not be empty")
	}
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	hp := hashedPassword{p, salt, hash}
	return encode(hp), nil
}

// VerifyPassword checks a plaintext password against an encoded Argon2id hash.
// It uses constant-time comparison to avoid timing attacks.
func VerifyPassword(password, encodedHash string) (bool, error) {
	hp, err := decode(encodedHash)
	if err != nil {
		return false, err
	}
	other := argon2.IDKey([]byte(password), hp.Salt, hp.Iterations, hp.Memory, hp.Parallelism, hp.KeyLength)
	if subtle.ConstantTimeCompare(hp.Hash, other) == 1 {
		return true, nil
	}
	return false, nil
}

func encode(hp hashedPassword) string {
	b64Salt := base64.RawStdEncoding.EncodeToString(hp.Salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hp.Hash)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, hp.Memory, hp.Iterations, hp.Parallelism, b64Salt, b64Hash)
}

func decode(encoded string) (*hashedPassword, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return nil, ErrInvalidHash
	}
	if parts[1] != "argon2id" {
		return nil, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return nil, ErrInvalidHash
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return nil, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, ErrInvalidHash
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, ErrInvalidHash
	}
	if len(salt) == 0 || len(hash) == 0 {
		return nil, ErrInvalidHash
	}
	p.SaltLength = uint32(len(salt))
	p.KeyLength = uint32(len(hash))
	return &hashedPassword{p, salt, hash}, nil
}
