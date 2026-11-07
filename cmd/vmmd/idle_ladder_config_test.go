package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261107122928 — the idle ladder is off by default; vmmd.toml can set
// observe, FAAS_IDLE_LADDER overrides TOML, and any other mode is refused.
func TestLoadConfig_IdleLadderMode(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IdleLadder != idleLadderOff {
		t.Fatalf("IdleLadder = %q, want off by default", cfg.IdleLadder)
	}
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte("idle_ladder = \"observe\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		env  string
		want string
	}{{"", idleLadderObserve}, {"off", idleLadderOff}, {" OBSERVE ", idleLadderObserve}} {
		t.Run("env="+tc.env, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("FAAS_IDLE_LADDER", tc.env)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.IdleLadder != tc.want {
				t.Fatalf("IdleLadder = %q, want %q", cfg.IdleLadder, tc.want)
			}
		})
	}
	for _, bad := range []string{"on", "true", "shadow"} {
		t.Setenv("FAAS_IDLE_LADDER", bad)
		if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "FAAS_IDLE_LADDER") {
			t.Fatalf("FAAS_IDLE_LADDER=%q error = %v, want a named validation error", bad, err)
		}
	}
}
