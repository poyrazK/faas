package main

import (
	"errors"
	"testing"

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
