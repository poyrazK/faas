// adr: 1027 — the native backend can be qualified and approved like Neon:
// the tooling loads a configuration that names it (beside Neon), describes
// it without reaching schedd, and a live run asks for the verifier database
// up front.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const nativeDefaultConfig = `{
  "default_region": "eu-central",
  "defaults": {"eu-central": "native-eu"},
  "max_databases_per_account": 3,
  "provisioning_enabled": false,
  "usage": {"enabled": false, "collection_interval_seconds": 300, "window_seconds": 3600, "stale_after_seconds": 10800,
    "max_monthly_cost_millicents": 0, "max_monthly_compute_unit_seconds": 0, "max_monthly_storage_byte_seconds": 0,
    "max_monthly_history_byte_seconds": 0, "max_monthly_egress_bytes": 0, "compute_unit_hour_millicents": 0,
    "storage_gib_hour_millicents": 0, "history_gib_hour_millicents": 0, "egress_gib_millicents": 0},
  "backends": [{
    "id": "native-eu", "driver": "firecracker", "region": "eu-central", "namespace": "native",
    "settings": {"host_suffix": "db.gregale.test", "max_restore_window_seconds": "604800"},
    "secret_env": {"credential-key": "FAAS_NATIVE_CREDENTIAL_KEY"}
  }, {
    "id": "neon-eu", "driver": "neon", "region": "eu-central", "namespace": "org-legacy",
    "settings": {"region_id": "aws-eu-central-1", "database_name": "gregale", "max_storage_bytes": "107374182400", "max_restore_window_seconds": "604800"},
    "secret_env": {"api-key": "FAAS_NEON_API_KEY"}
  }]
}`

func nativeEnv(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "managed-postgres.json")
	if err := os.WriteFile(path, []byte(nativeDefaultConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"FAAS_MANAGED_POSTGRES_CONFIG":              path,
		managedpostgres.EnvironmentEnv:              "staging",
		"FAAS_NEON_API_KEY":                         "preflight-only-secret",
		"FAAS_MANAGED_POSTGRES_QUALIFY_RESOURCE_ID": "native-preflight",
		managedpostgres.QualificationApprovalTTLEnv: "24h",
	}
}

// The native backend loads and is described without schedd, a database or
// its credential key. It is not yet ready to be the default: Scale plans are
// entitled to the production class, which needs a standby VM native does
// not run yet, and the preflight says exactly that.
func TestPreflightDescribesANativeDefaultBackend(t *testing.T) {
	values := nativeEnv(t)
	var output, errorOutput bytes.Buffer
	code := runConfigurationPreflight(func(k string) string { return values[k] }, &output, &errorOutput)
	var result configurationPreflightOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.BackendID != "native-eu" || result.Spec == nil || result.Spec.PostgresMajor != 18 {
		t.Fatalf("preflight = %+v; want the native backend qualified at its newest major", result)
	}
	if code != 1 || !slices.Equal(result.Readiness.Reasons, []string{"plan_entitlement_unsupported"}) {
		t.Fatalf("preflight exit=%d reasons=%v; want only the missing production class", code, result.Readiness.Reasons)
	}
}

func TestLiveQualificationOfNativeNeedsTheVerifierDatabase(t *testing.T) {
	values := nativeEnv(t)
	values["FAAS_MANAGED_POSTGRES_QUALIFY_LIVE"] = "true"
	values["FAAS_NATIVE_CREDENTIAL_KEY"] = strings.Repeat("k", 32)
	var output, errorOutput bytes.Buffer
	if code := run(func(k string) string { return values[k] }, &output, &errorOutput); code != 2 || !strings.Contains(errorOutput.String(), nativeDatabaseURLEnv) {
		t.Fatalf("run exit=%d stderr=%q; want a refusal naming %s", code, errorOutput.String(), nativeDatabaseURLEnv)
	}
}
