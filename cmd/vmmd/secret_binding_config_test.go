package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261023173624 — vmmd accepts unbound runtime secrets by default;
// FAAS_RUNTIME_SECRET_BINDING_REQUIRED (the contract step) refuses them.
func TestLoadConfig_RequireSecretBindingSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	for _, tc := range []struct {
		env      string
		required bool
	}{{"", false}, {"0", false}, {"false", false}, {"1", true}, {"true", true}} {
		t.Run("env="+tc.env, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("FAAS_RUNTIME_SECRET_BINDING_REQUIRED", tc.env)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.RequireSecretBinding != tc.required {
				t.Fatalf("RequireSecretBinding = %v, want %v", cfg.RequireSecretBinding, tc.required)
			}
		})
	}
	t.Setenv("FAAS_RUNTIME_SECRET_BINDING_REQUIRED", "account")
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "FAAS_RUNTIME_SECRET_BINDING_REQUIRED") {
		t.Fatalf("invalid FAAS_RUNTIME_SECRET_BINDING_REQUIRED error = %v, want named validation error", err)
	}
}
