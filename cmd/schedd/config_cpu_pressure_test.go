package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261030100950 — the CPU pressure backstop is on by default (ADR-1151
// amendment 2026-10-31); schedd.toml can turn it off and
// FAAS_CPU_PRESSURE_BACKSTOP overrides TOML in either direction.
func TestLoadConfig_CPUPressureBackstop(t *testing.T) {
	testDefaultOnSwitch(t, "cpu_pressure_backstop", "FAAS_CPU_PRESSURE_BACKSTOP",
		func(c *Config) bool { return c.CPUPressureBackstop })
}

// adr: 1151 — quiet pause admission is on by default (amendment
// 2026-10-31); schedd.toml can turn it off and FAAS_QUIET_PAUSE_ADMISSION
// overrides TOML in either direction.
func TestLoadConfig_QuietPauseAdmission(t *testing.T) {
	testDefaultOnSwitch(t, "quiet_pause_admission", "FAAS_QUIET_PAUSE_ADMISSION",
		func(c *Config) bool { return c.QuietPauseAdmission })
}

func testDefaultOnSwitch(t *testing.T, key, env string, get func(*Config) bool) {
	t.Helper()
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || !get(cfg) {
		t.Fatalf("%s default = %v, %v; want on", key, cfg, err)
	}
	path := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(path, []byte(key+" = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		env     string
		enabled bool
	}{{"", false}, {"1", true}, {"true", true}, {"0", false}} {
		t.Run(key+"/env="+tc.env, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv(env, tc.env)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if get(cfg) != tc.enabled {
				t.Fatalf("%s = %v, want %v", key, get(cfg), tc.enabled)
			}
		})
	}
	t.Setenv(env, "sometimes")
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), env) {
		t.Fatalf("invalid %s error = %v, want named validation error", env, err)
	}
}
