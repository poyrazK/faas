package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ADR-20261023171905: [meter] quiet_pause_unbilled is off by default,
// overridden by FAAS_QUIET_PAUSE_UNBILLED, and refuses unknown values with or
// without a config file.
func TestLoadConfig_QuietPauseUnbilled(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.toml")
	t.Setenv("FAAS_QUIET_PAUSE_UNBILLED", "")
	cfg, err := LoadConfig(missing)
	if err != nil || cfg.Meter.QuietPauseUnbilled != "" {
		t.Fatalf("default = %+v, %v; want off", cfg.Meter, err)
	}
	tomlPath := filepath.Join(t.TempDir(), "meterd.toml")
	if err := os.WriteFile(tomlPath, []byte("[meter]\nquiet_pause_unbilled = \"shadow\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.Meter.QuietPauseUnbilled != "shadow" {
		t.Fatalf("toml shadow: %+v, %v", cfg.Meter, err)
	}
	t.Setenv("FAAS_QUIET_PAUSE_UNBILLED", "on")
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.Meter.QuietPauseUnbilled != "on" {
		t.Fatalf("env over toml: %+v, %v", cfg.Meter, err)
	}
	t.Setenv("FAAS_QUIET_PAUSE_UNBILLED", "yes")
	for _, path := range []string{tomlPath, missing} {
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("FAAS_QUIET_PAUSE_UNBILLED=yes accepted (%s)", path)
		}
	}
}
