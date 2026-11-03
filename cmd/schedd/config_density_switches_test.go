package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 1151, 20261029151111, 20261030100950, 20261031143521 — quiet pause
// admission, measured CPU admission, the CPU pressure backstop (amendments
// of 2026-10-31) and proactive frozen compression are on by default;
// schedd.toml can turn each one off, and its
// FAAS_* variable overrides the file either way.
func TestLoadConfig_DensitySwitches(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.QuietPauseAdmission || !cfg.MeasuredCPUAdmission || !cfg.CPUPressureBackstop || !cfg.FrozenProactiveCompress {
		t.Fatalf("defaults: quiet_pause_admission=%t measured_cpu_admission=%t cpu_pressure_backstop=%t frozen_proactive_compress=%t; want all on",
			cfg.QuietPauseAdmission, cfg.MeasuredCPUAdmission, cfg.CPUPressureBackstop, cfg.FrozenProactiveCompress)
	}
	for _, tc := range []struct {
		key, env string
		on       func(*Config) bool
	}{
		{"quiet_pause_admission", "FAAS_QUIET_PAUSE_ADMISSION", func(c *Config) bool { return c.QuietPauseAdmission }},
		{"measured_cpu_admission", "FAAS_MEASURED_CPU_ADMISSION", func(c *Config) bool { return c.MeasuredCPUAdmission }},
		{"cpu_pressure_backstop", "FAAS_CPU_PRESSURE_BACKSTOP", func(c *Config) bool { return c.CPUPressureBackstop }},
		{"frozen_proactive_compress", "FAAS_FROZEN_PROACTIVE_COMPRESS", func(c *Config) bool { return c.FrozenProactiveCompress }},
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
			// A manifest render rewrites schedd.toml, so the lasting opt-out
			// is the environment, which overrides the file either way. (Like
			// every schedd override it applies once the rendered file is
			// read; LoadConfig returns defaults for a missing file.)
			emptyPath := filepath.Join(t.TempDir(), "schedd.toml")
			if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, env := range []struct {
				value string
				want  bool
			}{{"false", false}, {"0", false}, {"true", true}, {"1", true}} {
				t.Setenv(tc.env, env.value)
				for _, path := range []string{tomlPath, emptyPath} {
					cfg, err := LoadConfig(path)
					if err != nil || tc.on(cfg) != env.want {
						t.Fatalf("%s=%s with %s: on=%t, err=%v; want %t", tc.env, env.value, filepath.Base(path), err == nil && tc.on(cfg), err, env.want)
					}
				}
			}
			t.Setenv(tc.env, "sometimes")
			if _, err := LoadConfig(tomlPath); err == nil || !strings.Contains(err.Error(), tc.env) {
				t.Fatalf("invalid %s error = %v, want named validation error", tc.env, err)
			}
		})
	}
}
