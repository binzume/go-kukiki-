package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAuthConfigReadsSessionSecretFromEnvironment(t *testing.T) {
	t.Setenv(sessionSecretEnv, "environment-session-secret")
	config, err := LoadAuthConfig(filepath.Join(t.TempDir(), "auth.toml"))
	if err != nil {
		t.Fatalf("LoadAuthConfig returned an error: %v", err)
	}
	if config == nil || len(config.Accounts) != 0 || config.SessionSecret != "environment-session-secret" {
		t.Fatalf("auth config = %#v; want the environment session secret", config)
	}
}

func TestLoadAuthConfigGeneratesSessionSecretWhenEnvironmentIsUnset(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "auth.toml")
	if err := os.WriteFile(configPath, []byte("sessionTTL = 60\n"), 0600); err != nil {
		t.Fatalf("write auth config: %v", err)
	}

	t.Setenv(sessionSecretEnv, "")
	config, err := LoadAuthConfig(configPath)
	if err != nil {
		t.Fatalf("LoadAuthConfig returned an error: %v", err)
	}
	if config == nil || len(config.SessionSecret) != sessionSecretLength {
		t.Fatalf("generated session secret = %#v; want %d characters", config, sessionSecretLength)
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
