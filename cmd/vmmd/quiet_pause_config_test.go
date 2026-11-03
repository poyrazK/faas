package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 1151 — quiet pause is on by default (amendment 2026-10-27); vmmd.toml
// can turn it off and FAAS_QUIET_PAUSE overrides TOML in either direction.
func TestLoadConfig_QuietPauseSwitch(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.QuietPause {
		t.Fatal("quiet pause must default to on")
	}
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte("quiet_pause = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		env     string
		enabled bool
	}{{"", false}, {"1", true}, {"true", true}, {"0", false}, {"false", false}} {
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

// adr: 1151 — observe mode is off by default; vmmd.toml can turn it on and
// FAAS_QUIET_PAUSE_OBSERVE overrides TOML in either direction. Quiet pause is
// on by default and wins over observe mode, so observing needs it off.
func TestLoadConfig_QuietPauseObserveSwitch(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QuietPauseObserve {
		t.Fatal("quiet pause observe mode must default to off")
	}
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte("quiet_pause = false\nquiet_pause_observe = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		env     string
		enabled bool
	}{{"", true}, {"1", true}, {"0", false}} {
		t.Run("env="+tc.env, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("FAAS_QUIET_PAUSE_OBSERVE", tc.env)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.QuietPauseObserve != tc.enabled || cfg.QuietPause {
				t.Fatalf("QuietPauseObserve = %v, QuietPause = %v; want %v, false", cfg.QuietPauseObserve, cfg.QuietPause, tc.enabled)
			}
		})
	}
	t.Setenv("FAAS_QUIET_PAUSE_OBSERVE", "maybe")
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "FAAS_QUIET_PAUSE_OBSERVE") {
		t.Fatalf("invalid FAAS_QUIET_PAUSE_OBSERVE error = %v, want named validation error", err)
	}
}
