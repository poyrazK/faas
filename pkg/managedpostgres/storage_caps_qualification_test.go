// adr: 20261025094915 — storage `on` needs staging evidence that a branch at
// its limit refuses growing writes and stays readable and running.
package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func completeStorageCapEvidence() StorageCapEvidence {
	return StorageCapEvidence{SizeReported: true, WritesRefusedAtLimit: true, ReadsAllowedAtLimit: true,
		ComputeKeptRunning: true, WritesAllowedAfterRaise: true, ComputeQuotaPreserved: true}
}

func testStorageCapQualification(backend Backend) *StorageCapQualification {
	return &StorageCapQualification{Version: StorageCapQualificationVersion, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		CompletedAt: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), Evidence: completeStorageCapEvidence(),
		Checks: []QualificationCheck{{Name: "storage_cap_probe", Passed: true}}}
}

func TestStorageCapEvidenceValidate(t *testing.T) {
	if err := completeStorageCapEvidence().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, clear := range map[string]func(*StorageCapEvidence){
		"size":          func(e *StorageCapEvidence) { e.SizeReported = false },
		"refused":       func(e *StorageCapEvidence) { e.WritesRefusedAtLimit = false },
		"reads":         func(e *StorageCapEvidence) { e.ReadsAllowedAtLimit = false },
		"compute":       func(e *StorageCapEvidence) { e.ComputeKeptRunning = false },
		"raise":         func(e *StorageCapEvidence) { e.WritesAllowedAfterRaise = false },
		"compute quota": func(e *StorageCapEvidence) { e.ComputeQuotaPreserved = false },
	} {
		evidence := completeStorageCapEvidence()
		clear(&evidence)
		if evidence.Validate() == nil {
			t.Errorf("evidence without %s accepted", name)
		}
	}
}

func TestStorageCapQualificationArtifact(t *testing.T) {
	provider := &storageCapTestProvider{capTestProvider: &capTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}}}
	registry := testRegistry(t, provider, nil)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	write := func(t *testing.T, body string) string {
		path := filepath.Join(t.TempDir(), "storage-cap.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valid, err := json.Marshal(testStorageCapQualification(backend))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadStorageCapQualification(write(t, string(valid)))
	if err != nil || registry.VerifyStorageCapQualification(loaded) != nil {
		t.Fatalf("valid artifact: %v, %v", err, registry.VerifyStorageCapQualification(loaded))
	}
	if _, err := LoadStorageCapQualification(write(t, `{"version":1,"surprise":true}`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown field: %v", err)
	}
	if _, err := LoadStorageCapQualification(write(t, string(valid)+"{}")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("trailing document: %v", err)
	}
	computeArtifact, err := json.Marshal(testCapQualification(backend))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStorageCapQualification(write(t, string(computeArtifact))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a compute cap artifact read as storage evidence: %v", err)
	}
	for name, mutate := range map[string]func(*StorageCapQualification){
		"another backend": func(a *StorageCapQualification) { a.BackendFingerprint = "other" },
		"failed check": func(a *StorageCapQualification) {
			a.Checks = append(a.Checks, QualificationCheck{Name: "cleanup_resource"})
		},
		"no checks":        func(a *StorageCapQualification) { a.Checks = nil },
		"missing evidence": func(a *StorageCapQualification) { a.Evidence.ComputeKeptRunning = false },
		"unknown version":  func(a *StorageCapQualification) { a.Version = 2 },
		"never completed":  func(a *StorageCapQualification) { a.CompletedAt = time.Time{} },
	} {
		artifact := testStorageCapQualification(backend)
		mutate(artifact)
		if registry.VerifyStorageCapQualification(*artifact) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	plain := testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, nil)
	if err := plain.VerifyStorageCapQualification(*testStorageCapQualification(backend)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("provider without storage limits: %v", err)
	}
}

type storageCapQualificationProvider struct {
	*qualificationProvider
	evidence StorageCapEvidence
	probeErr error
	probed   bool
}

func (p *storageCapQualificationProvider) ProbeStorageCap(context.Context, string, CredentialMaterial) (StorageCapEvidence, error) {
	p.probed = true
	return p.evidence, p.probeErr
}

func TestQualifyProviderRunsTheStorageCapProbeOnlyWhenAsked(t *testing.T) {
	for _, kind := range []string{"not asked", "complete", "incomplete", "provider error", "missing prober"} {
		t.Run(kind, func(t *testing.T) {
			base := &qualificationProvider{capabilities: testCapabilities()}
			p := &storageCapQualificationProvider{qualificationProvider: base, evidence: completeStorageCapEvidence()}
			var provider Provider = p
			switch kind {
			case "incomplete":
				p.evidence.ComputeKeptRunning = false
			case "provider error":
				p.probeErr = ErrUnavailable
			case "missing prober":
				provider = base
			}
			report, err := QualifyProvider(t.Context(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "storage-cap-qualification",
				Spec: testSpec(), Mutating: true, Timeout: time.Minute, StorageCap: kind != "not asked"})
			switch kind {
			case "not asked":
				if err != nil || p.probed || report.StorageCap != nil {
					t.Fatalf("unasked probe: err %v probed %v evidence %+v", err, p.probed, report.StorageCap)
				}
			case "complete":
				if err != nil || report.StorageCap == nil || report.StorageCap.Validate() != nil {
					t.Fatalf("complete proof rejected: %v %+v", err, report.StorageCap)
				}
			default:
				if err == nil {
					t.Fatalf("%s admitted", kind)
				}
			}
			if !base.deleted {
				t.Fatal("qualification leaked its disposable resource")
			}
		})
	}
}
