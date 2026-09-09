package main

import (
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
