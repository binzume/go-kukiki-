package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAuthConfigMissingFileUsesEmptyConfig(t *testing.T) {
	config, err := LoadAuthConfig(filepath.Join(t.TempDir(), "auth.toml"))
	if err != nil {
		t.Fatalf("LoadAuthConfig returned an error: %v", err)
	}
	if config == nil || len(config.Accounts) != 0 || config.SessionSecret != "" {
		t.Fatalf("empty auth config = %#v", config)
	}
}

func TestSessionTTL(t *testing.T) {
	conf := &AuthConfig{
		SessionSecret: "test-session-secret",
		SessionTTL:    60,
		Accounts:      []AccountConfig{{Name: "admin"}},
	}
	account := &AccountConfig{Name: "admin"}

	current := conf.CreateSessionString(account)
	if got, err := conf.VerifySession(current); err != nil || got.Name != account.Name {
		t.Fatalf("current session verification = (%v, %v), want account %q", got, err, account.Name)
	}

	expired := genSessionStringAt(account.Name, time.Now().Add(-61*time.Second).Unix(), conf)
	if _, err := conf.VerifySession(expired); err == nil {
		t.Fatal("expired session was accepted")
	}
}
