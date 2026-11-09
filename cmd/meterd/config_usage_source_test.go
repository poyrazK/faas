package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/meter"
)

// ADR-20261107105126: [meter] billing_usage_source defaults to ledger_shadow,
// is overridden by FAAS_BILLING_USAGE_SOURCE, and refuses unknown values with
// or without a config file rather than guessing what to bill from.
func TestLoadConfig_BillingUsageSource(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.toml")
	t.Setenv("FAAS_QUIET_PAUSE_UNBILLED", "")
	t.Setenv("FAAS_BILLING_USAGE_SOURCE", "")
	cfg, err := LoadConfig(missing)
	if err != nil {
		t.Fatal(err)
	}
	if source, err := meter.ParseUsageSource(cfg.Meter.BillingUsageSource); err != nil || source != meter.UsageSourceLedgerShadow {
		t.Fatalf("default source = %q (%v), want ledger_shadow", source, err)
	}
	tomlPath := filepath.Join(t.TempDir(), "meterd.toml")
	if err := os.WriteFile(tomlPath, []byte("[meter]\nbilling_usage_source = \"off\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.Meter.BillingUsageSource != "off" {
		t.Fatalf("toml off: %+v, %v", cfg.Meter, err)
	}
	t.Setenv("FAAS_BILLING_USAGE_SOURCE", "ledger")
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.Meter.BillingUsageSource != "ledger" {
		t.Fatalf("env over toml: %+v, %v", cfg.Meter, err)
	}
	t.Setenv("FAAS_BILLING_USAGE_SOURCE", "rss")
	for _, path := range []string{tomlPath, missing} {
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("FAAS_BILLING_USAGE_SOURCE=rss accepted (%s)", path)
		}
	}
}
