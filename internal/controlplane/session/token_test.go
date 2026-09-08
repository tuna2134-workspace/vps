package session

import (
	"strings"
	"testing"
)

func TestNewTokenEntropy(t *testing.T) {
	tok, err := NewToken(32)
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	// 32 random bytes base64url encoded => ~43 chars.
	if len(tok.String()) < 40 {
		t.Errorf("token too short: %d", len(tok.String()))
	}
}

func TestTokensUnique(t *testing.T) {
	a, _ := NewToken(32)
	b, _ := NewToken(32)
	if a.String() == b.String() {
		t.Error("tokens must be unique")
	}
}

func TestTokenHash(t *testing.T) {
	tok, _ := NewToken(32)
	hash := tok.Hash()
	// SHA-256 hex => 64 chars.
	if len(hash) != 64 {
		t.Errorf("expected 64-char hex hash, got %d", len(hash))
	}
	if strings.Contains(hash, tok.String()) {
		t.Error("hash must not contain the raw token")
	}
}

func TestTokenRoundTrip(t *testing.T) {
	tok, _ := NewToken(32)
	restored := TokenFromString(tok.String())
	if restored.Hash() != tok.Hash() {
		t.Error("TokenFromString round-trip mismatch")
	}
}
