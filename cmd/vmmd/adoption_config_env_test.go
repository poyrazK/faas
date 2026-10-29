package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261017 vmmd restart adoption — the adoption switches can be set
// from the unit environment (ansible vmmd_adoption_record and friends); a
// set variable overrides vmmd.toml in either direction, and the switches'
// dependencies are checked on the result.
func TestLoadConfig_AdoptionSwitchesFromEnvironment(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.toml")
	cfg, err := LoadConfig(missing)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdoptionRecord || cfg.AdoptRunningVMs || cfg.AdoptAdmittedRuntimes {
		t.Fatalf("adoption switches must default to off: %+v", cfg)
	}

	t.Run("environment turns them on", func(t *testing.T) {
		t.Setenv("FAAS_VMMD_ADOPTION_RECORD", "true")
		t.Setenv("FAAS_VMMD_ADOPT_RUNNING_VMS", "1")
		t.Setenv("FAAS_VMMD_ADOPT_ADMITTED_RUNTIMES", "true")
		cfg, err := LoadConfig(missing)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.AdoptionRecord || !cfg.AdoptRunningVMs || !cfg.AdoptAdmittedRuntimes {
			t.Fatalf("switches = %v %v %v, want all on", cfg.AdoptionRecord, cfg.AdoptRunningVMs, cfg.AdoptAdmittedRuntimes)
		}
	})

	t.Run("environment overrides the TOML", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "vmmd.toml")
		if err := os.WriteFile(path, []byte("adoption_record = true\nadopt_running_vms = true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FAAS_VMMD_ADOPTION_RECORD", "true")
		t.Setenv("FAAS_VMMD_ADOPT_RUNNING_VMS", "false")
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.AdoptionRecord || cfg.AdoptRunningVMs {
			t.Fatalf("switches = %v %v, want record on and adoption off", cfg.AdoptionRecord, cfg.AdoptRunningVMs)
		}
	})

	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"not a boolean", map[string]string{"FAAS_VMMD_ADOPTION_RECORD": "sometimes"}, "FAAS_VMMD_ADOPTION_RECORD"},
		{"adoption without records", map[string]string{"FAAS_VMMD_ADOPT_RUNNING_VMS": "true"}, "adopt_running_vms requires adoption_record"},
		{"admitted without adoption", map[string]string{"FAAS_VMMD_ADOPTION_RECORD": "true", "FAAS_VMMD_ADOPT_ADMITTED_RUNTIMES": "true"},
			"adopt_admitted_runtimes requires adopt_running_vms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := LoadConfig(missing); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}
