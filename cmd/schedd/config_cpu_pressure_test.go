package main

import (
	"os"
	"path/filepath"
	"testing"
)

// adr: 20261030100950 — the CPU pressure backstop is off by default and
// schedd.toml can turn it on.
func TestLoadConfig_CPUPressureBackstop(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || cfg.CPUPressureBackstop {
		t.Fatalf("default = %v, %v; want off", cfg, err)
	}
	tomlPath := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(tomlPath, []byte("cpu_pressure_backstop = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || !cfg.CPUPressureBackstop {
		t.Fatalf("toml on: %v, %v", cfg, err)
	}
}
