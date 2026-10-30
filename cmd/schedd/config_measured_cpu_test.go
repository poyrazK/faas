package main

import (
	"os"
	"path/filepath"
	"testing"
)

// adr: 20261029151111 — measured CPU admission is off by default and
// schedd.toml can turn it on.
func TestLoadConfig_MeasuredCPUAdmission(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || cfg.MeasuredCPUAdmission {
		t.Fatalf("default = %v, %v; want off", cfg, err)
	}
	tomlPath := filepath.Join(t.TempDir(), "schedd.toml")
	if err := os.WriteFile(tomlPath, []byte("measured_cpu_admission = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || !cfg.MeasuredCPUAdmission {
		t.Fatalf("toml on: %v, %v", cfg, err)
	}
}
