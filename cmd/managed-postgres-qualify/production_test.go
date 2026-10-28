// adr: 20261028103115 — the read-only production check names the backend to
// pin and opens only for a complete approval.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestProductionCheckReportsTheBackendToPinAndApprovesAPinnedOne(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "managed-postgres.json")
	config := `{
  "default_region": "us-east-1",
  "defaults": {"us-east-1": "neon-us"},
  "provisioning_enabled": true,
  "usage": {
    "enabled": true, "collection_interval_seconds": 300, "window_seconds": 3600, "stale_after_seconds": 10800,
    "max_monthly_cost_millicents": 100000, "max_monthly_compute_unit_seconds": 360000,
    "max_monthly_storage_byte_seconds": 1000000000000, "max_monthly_egress_bytes": 1000000000
  },
  "fleet": {"max_databases": 50, "max_monthly_cost_millicents": 50000000},
  "backends": [{
    "id": "neon-us", "driver": "neon", "region": "us-east-1", "namespace": "org-production",
    "settings": {"region_id": "aws-us-east-1", "database_name": "gregale", "max_storage_bytes": "107374182400", "max_restore_window_seconds": "604800"},
    "secret_env": {"api-key": "FAAS_NEON_API_KEY"}
  }]
}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	values := map[string]string{
		"FAAS_MANAGED_POSTGRES_CONFIG": configPath,
		"FAAS_NEON_API_KEY":            "check-only-secret",
		managedpostgres.EnvironmentEnv: "production",
	}
	check := func() (int, map[string]any, string) {
		var output, errorOutput bytes.Buffer
		code := runProductionCheck(func(key string) string { return values[key] }, now, &output, &errorOutput)
		var result map[string]any
		_ = json.Unmarshal(output.Bytes(), &result)
		return code, result, errorOutput.String()
	}

	code, result, stderr := check()
	fingerprint, _ := result["configured_backend_fingerprint"].(string)
	if code != 1 || result["configured_backend_id"] != "neon-us" || len(fingerprint) != 64 || stderr == "" {
		t.Fatalf("unapproved check = %d %v %q", code, result, stderr)
	}

	values[managedpostgres.ProductionBackendEnv] = "neon-us"
	values[managedpostgres.ProductionFingerprintEnv] = fingerprint
	values[managedpostgres.ProductionApprovedAtEnv] = now.Add(-time.Hour).Format(time.RFC3339)
	values[managedpostgres.ProductionUntilEnv] = now.Add(30 * 24 * time.Hour).Format(time.RFC3339)
	if code, result, stderr = check(); code != 0 || result["readiness"].(map[string]any)["ready"] != true {
		t.Fatalf("approved check = %d %v %q", code, result, stderr)
	}
	if approval := result["approval"].(map[string]any); approval["backend_id"] != "neon-us" {
		t.Fatalf("approval echo = %v", approval)
	}

	values[managedpostgres.EnvironmentEnv] = "staging"
	if code, _, _ = check(); code != 2 {
		t.Fatalf("outside production = %d, want 2", code)
	}
}
