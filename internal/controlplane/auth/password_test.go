package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple", DefaultParams)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=") {
		t.Errorf("unexpected hash format: %s", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Error("hash must not contain plaintext")
	}

	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("expected password to verify")
	}

	ok, err = VerifyPassword("wrong", hash)
	if err != nil {
		t.Fatalf("VerifyPassword wrong: %v", err)
	}
	if ok {
		t.Error("wrong password must not verify")
	}
}

func TestHashIsUnique(t *testing.T) {
	// Same password must produce different hashes (random salt).
	h1, _ := HashPassword("same", DefaultParams)
	h2, _ := HashPassword("same", DefaultParams)
	if h1 == h2 {
		t.Error("hashes must differ due to random salt")
	}
}

func TestVerifyMalformed(t *testing.T) {
	if _, err := VerifyPassword("x", "not-a-hash"); err == nil {
		t.Error("expected error for malformed hash")
	}
	if _, err := VerifyPassword("x", "$argon2id$v=19$m=65536,t=3,p=2$badbase64$hash"); err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestHashPasswordEmpty(t *testing.T) {
	if _, err := HashPassword("", DefaultParams); err == nil {
		t.Error("empty password must be rejected")
	}
}
