// Package session implements opaque session tokens per the OWASP Session
// Management Cheat Sheet. Raw tokens are returned to the client only; the
// database stores their SHA-256 hash.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// Token is an opaque, high-entropy session token. The raw value is never
// stored or logged; only Hash() is persisted.
type Token struct {
	raw string
}

// NewToken generates a cryptographically random session token.
func NewToken(byteLen int) (*Token, error) {
	if byteLen < 16 {
		return nil, fmt.Errorf("session token length must be at least 16 bytes, got %d", byteLen)
	}
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate session token: %w", err)
	}
	return &Token{raw: base64.RawURLEncoding.EncodeToString(buf)}, nil
}

// TokenFromString restores a Token from its raw representation (used by the
// client-supplied Authorization header).
func TokenFromString(raw string) *Token {
	return &Token{raw: raw}
}

// String returns the raw token value. Use with care: never log this.
func (t *Token) String() string {
	return t.raw
}

// Hash returns the SHA-256 hex digest of the token for storage.
func (t *Token) Hash() string {
	sum := sha256.Sum256([]byte(t.raw))
	return hex.EncodeToString(sum[:])
}