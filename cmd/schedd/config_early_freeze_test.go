package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ADR-20261021215353: frozen_tier_early_freeze_after_s is off by default,
// bounded to 2-300 s, and overridden by its environment variable.
func TestLoadConfig_FrozenTierEarlyFreeze(t *testing.T) {
	t.Setenv("FAAS_FROZEN_TIER_EARLY_FREEZE_AFTER_S", "")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || cfg.FrozenTierEarlyFreezeAfterS != 0 {
		t.Fatalf("default = %v, %v; want off", cfg, err)
	}
	tomlPath := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(tomlPath, []byte("frozen_tier = true\nfrozen_tier_early_freeze_after_s = 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.FrozenTierEarlyFreezeAfterS != 10 {
		t.Fatalf("toml 10 s: %v, %v", cfg, err)
	}
	t.Setenv("FAAS_FROZEN_TIER_EARLY_FREEZE_AFTER_S", "30")
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.FrozenTierEarlyFreezeAfterS != 30 {
		t.Fatalf("env over toml: %v, %v", cfg, err)
	}
	for _, bad := range []string{"1", "301", "-5", "soon"} {
		t.Setenv("FAAS_FROZEN_TIER_EARLY_FREEZE_AFTER_S", bad)
		if _, err := LoadConfig(tomlPath); err == nil {
			t.Fatalf("FAAS_FROZEN_TIER_EARLY_FREEZE_AFTER_S=%s accepted", bad)
		}
	}
}
