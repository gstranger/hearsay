package hearsay_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".hearsay.toml")
	content := `version = 1
namespace = "org/repo/branch"
provider = "sqlite"

[provider_config.sqlite]
path = ".hearsay.db"

[defaults]
ttl_seconds = 300
claim_on_read = false
auto_heartbeat = true
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := hearsay.LoadConfig(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if cfg.Namespace != "org/repo/branch" {
		t.Fatalf("namespace mismatch: got %q", cfg.Namespace)
	}
	if cfg.Provider != "sqlite" {
		t.Fatalf("provider mismatch: got %q", cfg.Provider)
	}
	if cfg.ProviderCfg.SQLite == nil || cfg.ProviderCfg.SQLite.Path != ".hearsay.db" {
		t.Fatalf("sqlite config missing or wrong path")
	}
	if cfg.Defaults.TTLSeconds != 300 {
		t.Fatalf("ttl mismatch: got %d", cfg.Defaults.TTLSeconds)
	}
}

func TestSaveConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".hearsay.toml")
	cfg := &hearsay.Config{
		Version:   1,
		Namespace: "test",
		Provider:  "sqlite",
		ProviderCfg: hearsay.ProviderConfig{
			SQLite: &hearsay.SQLiteConfig{Path: "test.db"},
		},
		Defaults: hearsay.DefaultsConfig{TTLSeconds: 60},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := hearsay.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Namespace != "test" {
		t.Fatalf("save/load mismatch: got %q", loaded.Namespace)
	}
}

func TestLoadConfigA2A(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".hearsay.toml")
	content := `version = 1
namespace = "org/repo/branch"
provider = "sqlite"

[provider_config.sqlite]
path = ".hearsay.db"

[defaults]
ttl_seconds = 300
claim_on_read = false
auto_heartbeat = true

[a2a]
addr = "localhost:8081"
api_key = "ak_test"
bearer_jwks_url = "https://auth.example.com/jwks.json"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := hearsay.LoadConfig(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if cfg.A2A == nil {
		t.Fatal("expected A2A config to be present, got nil")
	}
	if cfg.A2A.Addr != "localhost:8081" {
		t.Fatalf("a2a addr mismatch: got %q", cfg.A2A.Addr)
	}
	if cfg.A2A.APIKey != "ak_test" {
		t.Fatalf("a2a api_key mismatch: got %q", cfg.A2A.APIKey)
	}
	if cfg.A2A.BearerJWKSURL != "https://auth.example.com/jwks.json" {
		t.Fatalf("a2a bearer_jwks_url mismatch: got %q", cfg.A2A.BearerJWKSURL)
	}
}
