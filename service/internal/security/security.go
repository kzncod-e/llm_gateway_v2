// Package security contains cryptographic primitives: credential encryption,
// gateway API key generation and hashing, session tokens and password hashing.
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// SecretBox encrypts provider credentials with AES-256-GCM.
// Ciphertext layout: version(1) || nonce(12) || sealed.
type SecretBox struct {
	aead cipher.AEAD
}

const secretBoxVersion = 1

func NewSecretBox(key []byte) (*SecretBox, error) {
	if len(key) != 32 {
		return nil, errors.New("encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretBox{aead: aead}, nil
}

// Seal encrypts plaintext. aad binds the ciphertext to a context (e.g. provider ID)
// so a ciphertext cannot be copied to another row.
func (s *SecretBox) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+s.aead.Overhead())
	out = append(out, secretBoxVersion)
	out = append(out, nonce...)
	return s.aead.Seal(out, nonce, plaintext, aad), nil
}

func (s *SecretBox) Open(ciphertext, aad []byte) ([]byte, error) {
	ns := s.aead.NonceSize()
	if len(ciphertext) < 1+ns+s.aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	if ciphertext[0] != secretBoxVersion {
		return nil, fmt.Errorf("unsupported ciphertext version %d", ciphertext[0])
	}
	nonce := ciphertext[1 : 1+ns]
	pt, err := s.aead.Open(nil, nonce, ciphertext[1+ns:], aad)
	if err != nil {
		return nil, errors.New("decrypt credential: authentication failed")
	}
	return pt, nil
}

// API keys --------------------------------------------------------------------

const APIKeyPrefix = "gwk_"

// KeyHasher derives lookup hashes for gateway API keys with a server-side pepper.
type KeyHasher struct{ pepper []byte }

func NewKeyHasher(pepper []byte) *KeyHasher { return &KeyHasher{pepper: pepper} }

func (h *KeyHasher) Hash(raw string) []byte {
	m := hmac.New(sha256.New, h.pepper)
	m.Write([]byte(raw))
	return m.Sum(nil)
}

// GenerateAPIKey returns a new raw key and its display prefix.
func GenerateAPIKey() (raw, display string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = APIKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	return raw, raw[:12], nil
}

// LooksLikeAPIKey performs a cheap format check before hashing/lookup.
func LooksLikeAPIKey(raw string) bool {
	return strings.HasPrefix(raw, APIKeyPrefix) && len(raw) == len(APIKeyPrefix)+43
}

// Session tokens ----------------------------------------------------------------

func NewSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func HashToken(token string) []byte {
	s := sha256.Sum256([]byte(token))
	return s[:]
}

// Passwords ---------------------------------------------------------------------

const bcryptCost = 12

func HashPassword(pw string) (string, error) {
	if len(pw) < 12 {
		return "", errors.New("password must be at least 12 characters")
	}
	if len(pw) > 72 {
		return "", errors.New("password must be at most 72 bytes")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// dummyHash is compared against when a user does not exist so that login timing
// does not reveal whether an email is registered.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalizer-password"), bcryptCost)

func EqualizeTiming(pw string) { _ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pw)) }

// Last4 returns the last four characters of a secret for display.
func Last4(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("•", len(s))
	}
	return s[len(s)-4:]
}
