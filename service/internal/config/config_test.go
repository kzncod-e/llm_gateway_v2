package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func setValid(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("API_KEY_PEPPER", strings.Repeat("p", 32))
}

func TestLoadDefaults(t *testing.T) {
	setValid(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.HTTPAddr != ":8080" || c.DefaultTeamRPM != 600 || c.StreamTimeout != 10*time.Minute {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if len(c.EncryptionKey) != 32 {
		t.Fatalf("encryption key not decoded")
	}
}

func TestLoadMissingRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("ENCRYPTION_KEY", "")
	t.Setenv("API_KEY_PEPPER", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"DATABASE_URL", "ENCRYPTION_KEY", "API_KEY_PEPPER"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestLoadInvalidValues(t *testing.T) {
	setValid(t)
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	t.Setenv("DEFAULT_TEAM_RPM", "abc")
	t.Setenv("STREAM_TIMEOUT", "-1s")
	t.Setenv("ADMIN_EMAIL", "a@b.c")
	t.Setenv("ADMIN_PASSWORD", "short")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"ENCRYPTION_KEY", "DEFAULT_TEAM_RPM", "STREAM_TIMEOUT", "ADMIN_PASSWORD"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}
