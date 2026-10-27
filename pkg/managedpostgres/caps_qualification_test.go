// adr: 20261025094915 — enforcement turns on only with staging evidence that
// the provider suspends at a quota in the ledger's unit and period.
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

func completeComputeCapEvidence() ComputeCapEvidence {
	return ComputeCapEvidence{PeriodStartsAtUTCMonth: true, CounterInComputeUnits: true, ComputeUnitRatioPermille: 1000,
		SuspendedAtQuota: true, RefusedWhileSuspended: true, ResumedAfterRaise: true, StorageLimitPreserved: true}
}

func testCapQualification(backend Backend) *ComputeCapQualification {
	return &ComputeCapQualification{Version: ComputeCapQualificationVersion, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		CompletedAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), Evidence: completeComputeCapEvidence(),
		Checks: []QualificationCheck{{Name: "compute_cap_probe", Passed: true}}}
}

func TestComputeUnitRatioAcceptable(t *testing.T) {
	for _, tc := range []struct {
		permille   int64
		min, max   float64
		acceptable bool
	}{
		{1000, 0.25, 1, true},
		{800, 0.25, 1, true},
		{5000, 0.25, 1, true}, // autoscaled to the largest size, within slack
		{799, 0.25, 1, false}, // a usage-based counter undercounts allocation
		{50, 0.25, 1, false},
		{5001, 0.25, 1, false},
		{1000, 0, 1, false},
	} {
		if got := ComputeUnitRatioAcceptable(tc.permille, tc.min, tc.max); got != tc.acceptable {
			t.Errorf("ratio %d‰ for %v–%v CU = %v, want %v", tc.permille, tc.min, tc.max, got, tc.acceptable)
		}
	}
}

func TestComputeCapEvidenceNeedsEveryObservation(t *testing.T) {
	if err := completeComputeCapEvidence().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, drop := range []func(*ComputeCapEvidence){
		func(e *ComputeCapEvidence) { e.PeriodStartsAtUTCMonth = false },
		func(e *ComputeCapEvidence) { e.CounterInComputeUnits = false },
		func(e *ComputeCapEvidence) { e.SuspendedAtQuota = false },
		func(e *ComputeCapEvidence) { e.RefusedWhileSuspended = false },
		func(e *ComputeCapEvidence) { e.ResumedAfterRaise = false },
		func(e *ComputeCapEvidence) { e.StorageLimitPreserved = false },
	} {
		evidence := completeComputeCapEvidence()
		drop(&evidence)
		if evidence.Validate() == nil {
			t.Fatalf("incomplete evidence accepted: %+v", evidence)
		}
	}
}

func TestComputeCapQualificationArtifact(t *testing.T) {
	provider := &capTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}}
	registry := testRegistry(t, provider, nil)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	write := func(t *testing.T, body string) string {
		path := filepath.Join(t.TempDir(), "cap.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valid, err := json.Marshal(testCapQualification(backend))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadComputeCapQualification(write(t, string(valid)))
	if err != nil || registry.VerifyComputeCapQualification(loaded) != nil {
		t.Fatalf("valid artifact: %v, %v", err, registry.VerifyComputeCapQualification(loaded))
	}
	if _, err := LoadComputeCapQualification(write(t, `{"version":1,"surprise":true}`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown field: %v", err)
	}
	if _, err := LoadComputeCapQualification(write(t, string(valid)+"{}")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("trailing document: %v", err)
	}
	for name, mutate := range map[string]func(*ComputeCapQualification){
		"another backend": func(a *ComputeCapQualification) { a.BackendFingerprint = "other" },
		"failed check": func(a *ComputeCapQualification) {
			a.Checks = append(a.Checks, QualificationCheck{Name: "cleanup_resource"})
		},
		"no checks":        func(a *ComputeCapQualification) { a.Checks = nil },
		"missing evidence": func(a *ComputeCapQualification) { a.Evidence.ResumedAfterRaise = false },
		"unknown version":  func(a *ComputeCapQualification) { a.Version = 2 },
		"never completed":  func(a *ComputeCapQualification) { a.CompletedAt = time.Time{} },
	} {
		artifact := testCapQualification(backend)
		mutate(artifact)
		if registry.VerifyComputeCapQualification(*artifact) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	plain := testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, nil)
	if err := plain.VerifyComputeCapQualification(*testCapQualification(backend)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("provider without quotas: %v", err)
	}
}

type capQualificationProvider struct {
	*qualificationProvider
	evidence ComputeCapEvidence
	probeErr error
	probed   bool
}

func (p *capQualificationProvider) ProbeComputeCap(context.Context, string, CredentialMaterial) (ComputeCapEvidence, error) {
	p.probed = true
	return p.evidence, p.probeErr
}

func TestQualifyProviderRunsTheComputeCapProbeOnlyWhenAsked(t *testing.T) {
	for _, kind := range []string{"not asked", "complete", "incomplete", "provider error", "missing prober"} {
		t.Run(kind, func(t *testing.T) {
			base := &qualificationProvider{capabilities: testCapabilities()}
			p := &capQualificationProvider{qualificationProvider: base, evidence: completeComputeCapEvidence()}
			var provider Provider = p
			switch kind {
			case "incomplete":
				p.evidence.SuspendedAtQuota = false
			case "provider error":
				p.probeErr = ErrUnavailable
			case "missing prober":
				provider = base
			}
			report, err := QualifyProvider(t.Context(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "cap-qualification",
				Spec: testSpec(), Mutating: true, Timeout: time.Minute, ComputeCap: kind != "not asked"})
			switch kind {
			case "not asked":
				if err != nil || p.probed || report.ComputeCap != nil {
					t.Fatalf("unasked probe: err %v probed %v evidence %+v", err, p.probed, report.ComputeCap)
				}
			case "complete":
				if err != nil || report.ComputeCap == nil || report.ComputeCap.Validate() != nil {
					t.Fatalf("complete proof rejected: %v %+v", err, report.ComputeCap)
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

func TestCapEnforcerOnNeedsQualification(t *testing.T) {
	provider := &capTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}}
	registry := testRegistry(t, provider, func(config *Config) {
		config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
			StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
			MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
	})
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	ceiling := func(context.Context, string) (int64, error) { return 1, nil }
	if _, err := NewCapEnforcer(registry, &capTestStore{}, CapEnforcerOptions{Mode: CapEnforcementOn, Ceiling: ceiling}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("on without qualification: %v", err)
	}
	failed := testCapQualification(backend)
	failed.Evidence.RefusedWhileSuspended = false
	if _, err := NewCapEnforcer(registry, &capTestStore{}, CapEnforcerOptions{Mode: CapEnforcementOn, Ceiling: ceiling, Qualification: failed}); err == nil {
		t.Fatal("on with failed evidence started")
	}
	if _, err := NewCapEnforcer(registry, &capTestStore{}, CapEnforcerOptions{Mode: CapEnforcementOn, Ceiling: ceiling, Qualification: testCapQualification(backend)}); err != nil {
		t.Fatalf("on with qualification: %v", err)
	}
}
