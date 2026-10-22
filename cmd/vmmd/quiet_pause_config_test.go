package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 1151 — quiet pause is off by default; vmmd.toml can turn it on and
// FAAS_QUIET_PAUSE overrides TOML in either direction.
func TestLoadConfig_QuietPauseSwitch(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QuietPause {
		t.Fatal("quiet pause must default to off")
	}
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte("quiet_pause = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		env     string
		enabled bool
	}{{"", true}, {"1", true}, {"true", true}, {"0", false}, {"false", false}} {
		t.Run("env="+tc.env, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("FAAS_QUIET_PAUSE", tc.env)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.QuietPause != tc.enabled {
				t.Fatalf("QuietPause = %v, want %v", cfg.QuietPause, tc.enabled)
			}
		})
	}
	t.Setenv("FAAS_QUIET_PAUSE", "sometimes")
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "FAAS_QUIET_PAUSE") {
		t.Fatalf("invalid FAAS_QUIET_PAUSE error = %v, want named validation error", err)
	}
}
