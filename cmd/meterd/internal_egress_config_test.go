// adr: 20261114100000
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// FAAS_INTERNAL_EGRESS_UNBILLED overrides [meter] internal_egress_unbilled
// and refuses a value that is not a boolean.
func TestLoadConfig_InternalEgressUnbilled(t *testing.T) {
	t.Setenv("FAAS_QUIET_PAUSE_UNBILLED", "")
	t.Setenv("FAAS_BILLING_USAGE_SOURCE", "")
	t.Setenv("FAAS_GATEWAY_RESPONSE_EGRESS", "")
	t.Setenv("FAAS_INTERNAL_EGRESS_UNBILLED", "")
	dir := t.TempDir()
	cfg, err := LoadConfig(filepath.Join(dir, "missing.toml"))
	if err != nil || cfg.Meter.InternalEgressUnbilled {
		t.Fatalf("default = %+v (%v), want off", cfg, err)
	}
	tomlPath := filepath.Join(dir, "meterd.toml")
	if err := os.WriteFile(tomlPath, []byte("[meter]\ninternal_egress_unbilled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || !cfg.Meter.InternalEgressUnbilled {
		t.Fatalf("toml on: %+v, %v", cfg, err)
	}
	t.Setenv("FAAS_INTERNAL_EGRESS_UNBILLED", "false")
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.Meter.InternalEgressUnbilled {
		t.Fatalf("env over toml: %+v, %v", cfg, err)
	}
	t.Setenv("FAAS_INTERNAL_EGRESS_UNBILLED", "true")
	if cfg, err = LoadConfig(filepath.Join(dir, "missing.toml")); err != nil || !cfg.Meter.InternalEgressUnbilled {
		t.Fatalf("env on: %+v, %v", cfg, err)
	}
	t.Setenv("FAAS_INTERNAL_EGRESS_UNBILLED", "maybe")
	if _, err := LoadConfig(tomlPath); err == nil {
		t.Fatal("FAAS_INTERNAL_EGRESS_UNBILLED=maybe accepted")
	}
}
