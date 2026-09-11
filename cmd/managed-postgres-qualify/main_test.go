package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestIsLiveQualificationEnabledFailsClosed(t *testing.T) {
	values := map[string]string{
		managedpostgres.EnvironmentEnv:       "staging",
		"FAAS_MANAGED_POSTGRES_QUALIFY_LIVE": "true",
	}
	if !isLiveQualificationEnabled(func(key string) string { return values[key] }) {
		t.Fatal("expected explicit staging qualification gate to open")
	}
	values[managedpostgres.EnvironmentEnv] = "production"
	if isLiveQualificationEnabled(func(key string) string { return values[key] }) {
		t.Fatal("qualification gate opened outside staging")
	}
}

func TestIsLifecycleQualificationEnabledRequiresLiveStagingGate(t *testing.T) {
	values := map[string]string{
		managedpostgres.EnvironmentEnv:            "staging",
		"FAAS_MANAGED_POSTGRES_QUALIFY_LIVE":      "true",
		"FAAS_MANAGED_POSTGRES_QUALIFY_LIFECYCLE": "true",
	}
	if !isLifecycleQualificationEnabled(func(key string) string { return values[key] }) {
		t.Fatal("expected lifecycle qualification gate to open")
	}
	values[managedpostgres.EnvironmentEnv] = "production"
	if isLifecycleQualificationEnabled(func(key string) string { return values[key] }) {
		t.Fatal("lifecycle qualification gate opened outside staging")
	}
	values[managedpostgres.EnvironmentEnv] = "staging"
	values["FAAS_MANAGED_POSTGRES_QUALIFY_LIVE"] = "false"
	if isLifecycleQualificationEnabled(func(key string) string { return values[key] }) {
		t.Fatal("lifecycle qualification gate opened without live qualification")
	}
}

func TestQualificationSpecUsesConservativeCapabilities(t *testing.T) {
	backend := managedpostgres.Backend{
		Region: "eu-central-1",
		Capabilities: managedpostgres.Capabilities{
			PostgresMajors:          []int{16, 17},
			ServiceClasses:          []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment},
			Availability:            []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone},
			ScaleToZero:             true,
			PointInTimeRestore:      true,
			MaxRestoreWindowSeconds: 3600,
			MaxStorageBytes:         2 << 30,
			UsageMeters:             []managedpostgres.Meter{managedpostgres.MeterComputeUnitSeconds},
		},
	}
	spec, err := qualificationSpec(backend, backend.Region)
	if err != nil {
		t.Fatal(err)
	}
	if spec.PostgresMajor != 17 || spec.StorageLimitBytes != 1<<30 || spec.RestoreWindowSeconds != 3600 || !spec.ScaleToZero {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestQualificationSpecRequiresScaleToZero(t *testing.T) {
	backend := managedpostgres.Backend{
		Region: "eu-central-1",
		Capabilities: managedpostgres.Capabilities{
			PostgresMajors:     []int{16},
			ServiceClasses:     []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment},
			Availability:       []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone},
			PointInTimeRestore: false,
			MaxStorageBytes:    2 << 30,
			UsageMeters:        []managedpostgres.Meter{managedpostgres.MeterComputeUnitSeconds},
		},
	}
	if _, err := qualificationSpec(backend, backend.Region); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("qualificationSpec error = %v, want ErrUnsupported", err)
	}
}

func TestQualificationApprovalTTLIsBounded(t *testing.T) {
	values := map[string]string{managedpostgres.QualificationApprovalTTLEnv: "36h"}
	if got := qualificationApprovalTTL(func(key string) string { return values[key] }); got != 36*time.Hour {
		t.Fatalf("ttl = %s", got)
	}
	for _, raw := range []string{"0s", "2161h", "not-a-duration"} {
		values[managedpostgres.QualificationApprovalTTLEnv] = raw
		if got := qualificationApprovalTTL(func(key string) string { return values[key] }); got != qualificationApprovalTTLDefault {
			t.Fatalf("ttl(%q) = %s, want default", raw, got)
		}
		if _, err := parseQualificationApprovalTTL(func(key string) string { return values[key] }); err == nil {
			t.Fatalf("parse ttl(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestApprovalEnvironmentContainsOnlyGateValues(t *testing.T) {
	approval := managedpostgres.QualificationApproval{
		BackendID:          "backend-a",
		BackendFingerprint: "fingerprint-a",
		ExpiresAt:          time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		CanaryAccounts:     []string{"account-a", "account-b"},
	}
	values := approvalEnvironment(approval)
	if values[managedpostgres.QualificationEnv] != "true" || values[managedpostgres.QualificationBackendEnv] != "backend-a" || values[managedpostgres.QualificationFingerprintEnv] != "fingerprint-a" || values[managedpostgres.QualificationUntilEnv] != "2026-09-09T12:00:00Z" || values[managedpostgres.CanaryAccountsEnv] != "account-a,account-b" {
		t.Fatalf("approval environment = %v", values)
	}
	if len(values) != 5 {
		t.Fatalf("approval environment has unexpected values: %v", values)
	}
}

func TestReadQualificationArtifactRejectsTrailingData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approval.json")
	artifact := managedpostgres.QualificationArtifact{Version: managedpostgres.QualificationArtifactVersion}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, []byte("\n{}\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readQualificationArtifact(path); err == nil {
		t.Fatal("trailing artifact data was accepted")
	}
}

func TestConfigurationPreflightIsProviderFreeAndReportsWarnings(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "managed-postgres.json")
	config := `{
  "default_region": "eu-central-1",
  "defaults": {"eu-central-1": "neon-eu"},
  "max_databases_per_account": 3,
  "provisioning_enabled": false,
  "usage": {
    "enabled": false,
    "collection_interval_seconds": 300,
    "window_seconds": 3600,
    "stale_after_seconds": 10800,
    "max_monthly_cost_millicents": 0,
    "max_monthly_compute_unit_seconds": 0,
    "max_monthly_storage_byte_seconds": 0,
    "max_monthly_history_byte_seconds": 0,
    "max_monthly_egress_bytes": 0,
    "compute_unit_hour_millicents": 0,
    "storage_gib_hour_millicents": 0,
    "history_gib_hour_millicents": 0,
    "egress_gib_millicents": 0
  },
  "backends": [{
    "id": "neon-eu",
    "driver": "neon",
    "region": "eu-central-1",
    "namespace": "org-preflight",
    "settings": {
      "region_id": "aws-eu-central-1",
      "database_name": "gregale",
      "max_storage_bytes": "107374182400",
      "max_restore_window_seconds": "604800"
    },
    "secret_env": {"api-key": "FAAS_NEON_API_KEY"}
  }]
}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"FAAS_MANAGED_POSTGRES_CONFIG":              configPath,
		managedpostgres.EnvironmentEnv:              "staging",
		"FAAS_NEON_API_KEY":                         "preflight-only-secret",
		"FAAS_MANAGED_POSTGRES_QUALIFY_RESOURCE_ID": "preflight-resource",
		"FAAS_MANAGED_POSTGRES_QUALIFY_TIMEOUT":     "10m",
		managedpostgres.QualificationApprovalTTLEnv: "24h",
		managedpostgres.CanaryAccountsEnv:           "account-a,account-b",
	}
	var output, errorOutput bytes.Buffer
	if exitCode := runConfigurationPreflight(func(key string) string { return values[key] }, &output, &errorOutput); exitCode != 0 {
		t.Fatalf("preflight exit=%d stderr=%q stdout=%s", exitCode, errorOutput.String(), output.String())
	}
	var result configurationPreflightOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	if !result.Readiness.Ready || result.BackendID != "neon-eu" || result.Spec == nil {
		t.Fatalf("preflight result = %+v", result)
	}
	if !containsString(result.Warnings, "usage_policy_disabled") || !containsString(result.Warnings, "restore_usage_not_isolated") {
		t.Fatalf("preflight warnings = %v", result.Warnings)
	}
}

func TestConfigurationPreflightFailsClosedBeforeLoadingProvider(t *testing.T) {
	values := map[string]string{
		managedpostgres.EnvironmentEnv: "production",
	}
	var output, errorOutput bytes.Buffer
	if exitCode := runConfigurationPreflight(func(key string) string { return values[key] }, &output, &errorOutput); exitCode == 0 {
		t.Fatalf("preflight unexpectedly passed: stdout=%s stderr=%s", output.String(), errorOutput.String())
	}
	var result configurationPreflightOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	if result.Readiness.Ready || !containsString(result.Readiness.Reasons, "configuration_path_missing") || !containsString(result.Readiness.Reasons, "environment_not_staging") {
		t.Fatalf("preflight readiness = %+v", result.Readiness)
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
