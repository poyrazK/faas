package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ADR-20261023171905: quiet_pause_ledger is off by default and overridden by
// its environment variable.
func TestLoadConfig_QuietPauseLedger(t *testing.T) {
	t.Setenv("FAAS_QUIET_PAUSE_LEDGER", "")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || cfg.QuietPauseLedger {
		t.Fatalf("default = %v, %v; want off", cfg, err)
	}
	tomlPath := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(tomlPath, []byte("quiet_pause_ledger = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || !cfg.QuietPauseLedger {
		t.Fatalf("toml on: %v, %v", cfg, err)
	}
	t.Setenv("FAAS_QUIET_PAUSE_LEDGER", "false")
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.QuietPauseLedger {
		t.Fatalf("env over toml: %v, %v", cfg, err)
	}
	t.Setenv("FAAS_QUIET_PAUSE_LEDGER", "sometimes")
	if _, err := LoadConfig(tomlPath); err == nil {
		t.Fatal("FAAS_QUIET_PAUSE_LEDGER=sometimes accepted")
	}
}
