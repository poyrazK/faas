package main

import (
	"os"
	"path/filepath"
	"testing"
)

// adr: 1151, 20261029151111, 20261030100950 — quiet pause admission,
// measured CPU admission and the CPU pressure backstop are on by default
// (amendments of 2026-10-31), and schedd.toml can turn each one off.
func TestLoadConfig_DensitySwitches(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.QuietPauseAdmission || !cfg.MeasuredCPUAdmission || !cfg.CPUPressureBackstop {
		t.Fatalf("defaults: quiet_pause_admission=%t measured_cpu_admission=%t cpu_pressure_backstop=%t; want all on",
			cfg.QuietPauseAdmission, cfg.MeasuredCPUAdmission, cfg.CPUPressureBackstop)
	}
	for _, tc := range []struct {
		key string
		on  func(*Config) bool
	}{
		{"quiet_pause_admission", func(c *Config) bool { return c.QuietPauseAdmission }},
		{"measured_cpu_admission", func(c *Config) bool { return c.MeasuredCPUAdmission }},
		{"cpu_pressure_backstop", func(c *Config) bool { return c.CPUPressureBackstop }},
	} {
		t.Run(tc.key, func(t *testing.T) {
			tomlPath := filepath.Join(t.TempDir(), "schedd.toml")
			if err := os.WriteFile(tomlPath, []byte(tc.key+" = false\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(tomlPath)
			if err != nil || tc.on(cfg) {
				t.Fatalf("%s = false: on=%t, err=%v; want off", tc.key, err == nil && tc.on(cfg), err)
			}
		})
	}
}
