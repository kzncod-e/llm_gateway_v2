package security

import (
	"bytes"
	"strings"
	"testing"
)

func TestSecretBoxRoundTripAndTamper(t *testing.T) {
	box, err := NewSecretBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ct, err := box.Seal([]byte("sk-secret"), []byte("provider-1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("sk-secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	pt, err := box.Open(ct, []byte("provider-1"))
	if err != nil || string(pt) != "sk-secret" {
		t.Fatalf("round trip failed: %v %q", err, pt)
	}
	if _, err := box.Open(ct, []byte("provider-2")); err == nil {
		t.Fatal("expected failure with different aad")
	}
	ct[len(ct)-1] ^= 1
	if _, err := box.Open(ct, []byte("provider-1")); err == nil {
		t.Fatal("expected failure on tampered ciphertext")
	}
	ct2, _ := box.Seal([]byte("sk-secret"), []byte("provider-1"))
	if bytes.Equal(ct, ct2) {
		t.Fatal("nonces must differ")
	}
}

func TestAPIKeyGeneration(t *testing.T) {
	raw, display, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !LooksLikeAPIKey(raw) || !strings.HasPrefix(raw, display) || len(display) != 12 {
		t.Fatalf("bad key %q display %q", raw, display)
	}
	raw2, _, _ := GenerateAPIKey()
	if raw == raw2 {
		t.Fatal("keys must be unique")
	}
	h := NewKeyHasher([]byte("pepper-pepper-pepper-pepper-pepper"))
	if !bytes.Equal(h.Hash(raw), h.Hash(raw)) || bytes.Equal(h.Hash(raw), h.Hash(raw2)) {
		t.Fatal("hash must be deterministic and distinct")
	}
	other := NewKeyHasher([]byte("another-pepper-another-pepper-xx"))
	if bytes.Equal(h.Hash(raw), other.Hash(raw)) {
		t.Fatal("pepper must affect hash")
	}
	if LooksLikeAPIKey("sk-abc") {
		t.Fatal("foreign key accepted")
	}
}

func TestPasswords(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse battery") || CheckPassword(h, "wrong password!!") {
		t.Fatal("password check mismatch")
	}
}
