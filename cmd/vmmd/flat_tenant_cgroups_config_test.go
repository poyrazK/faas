package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261109172421 — step 6.5: flat_tenant_cgroups defaults off, the unit
// environment (ansible vmmd_flat_tenant_cgroups) overrides vmmd.toml, and it
// is refused with native process recovery.
func TestLoadConfig_FlatTenantCgroups(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.toml")
	cfg, err := LoadConfig(missing)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FlatTenantCgroups {
		t.Fatal("flat_tenant_cgroups must default to off")
	}

	t.Run("environment turns it on", func(t *testing.T) {
		t.Setenv("FAAS_VMMD_FLAT_TENANT_CGROUPS", "true")
		cfg, err := LoadConfig(missing)
		if err != nil || !cfg.FlatTenantCgroups {
			t.Fatalf("flat = %v, err %v; want on", cfg != nil && cfg.FlatTenantCgroups, err)
		}
	})

	t.Run("environment overrides the TOML", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "vmmd.toml")
		if err := os.WriteFile(path, []byte("flat_tenant_cgroups = true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FAAS_VMMD_FLAT_TENANT_CGROUPS", "false")
		cfg, err := LoadConfig(path)
		if err != nil || cfg.FlatTenantCgroups {
			t.Fatalf("flat = %v, err %v; want off", cfg != nil && cfg.FlatTenantCgroups, err)
		}
	})

	t.Run("not with native process recovery", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "vmmd.toml")
		if err := os.WriteFile(path, []byte("native_process_recovery = true\nflat_tenant_cgroups = true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "flat_tenant_cgroups is not supported with native_process_recovery") {
			t.Fatalf("err = %v, want the native recovery refusal", err)
		}
	})
}
