package managedpostgres

import (
	"context"
	"testing"
	"time"
)

type recoveryQualificationProvider struct {
	*qualificationProvider
	fault  string
	source string
}

func (p *recoveryQualificationProvider) Inspect(ctx context.Context, id string) (ObservedDatabase, error) {
	o, err := p.qualificationProvider.Inspect(ctx, id)
	if p.fault != "missing pin" {
		o.DataResourceID = id + "/pinned"
	}
	return o, err
}
func (p *recoveryQualificationProvider) Restore(ctx context.Context, r RestoreRequest) (ObservedDatabase, error) {
	p.source = r.SourceResourceID
	return p.qualificationProvider.Restore(ctx, r)
}
func (p *recoveryQualificationProvider) ObserveRestoreSource(_ context.Context, d RestoreSourceDefinition) (RestoreSourceObservation, error) {
	o := RestoreSourceObservation{ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID, Status: ProviderStatusReady, RetentionSeconds: 3600, HistoryNotBefore: time.Now().UTC().Add(-time.Hour)}
	switch p.fault {
	case "missing history":
		o.HistoryNotBefore = time.Time{}
	case "wrong pin":
		o.DataResourceID = "replacement"
	case "disabled":
		o.RetentionSeconds = 0
	case "unavailable":
		return o, ErrUnavailable
	}
	return o, nil
}

func TestRestorePreflightQualificationBindsActualRestore(t *testing.T) {
	for _, fault := range []string{"complete", "missing observer", "missing pin", "missing history", "wrong pin", "disabled", "unavailable", "disabled spec"} {
		t.Run(fault, func(t *testing.T) {
			caps := testCapabilities()
			caps.RestorePreflight = true
			base := &qualificationProvider{capabilities: caps}
			p := &recoveryQualificationProvider{qualificationProvider: base, fault: fault}
			var provider Provider = p
			if fault == "missing observer" {
				provider = base
			}
			spec := testSpec()
			if fault == "disabled spec" {
				spec.RestoreWindowSeconds = 0
			}
			report, err := QualifyProvider(t.Context(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "recovery-proof", Spec: spec, Mutating: true})
			if fault == "complete" {
				if err != nil || ValidateQualificationReport(report) != nil || !report.RestorePreflight || report.Recovery == nil || p.source != base.resourceID+"/pinned" {
					t.Fatal(report, err, p.source)
				}
				registry := testRegistry(t, p, nil)
				backend, _ := registry.Default(registry.DefaultRegion)
				now := report.CompletedAt.Add(time.Minute)
				lifecycle := passingLifecycleQualificationReport()
				approval, err := BuildQualificationApproval(report, &lifecycle, backend.ID, backend.Fingerprint, nil, now, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				artifact := QualificationArtifact{Version: QualificationArtifactVersion, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Spec: spec, Report: report, Lifecycle: &lifecycle, Approval: &approval}
				if ready := registry.VerifyQualificationArtifact(artifact, nil, now); !ready.Ready {
					t.Fatal(ready)
				}
				artifact.Version = 6
				if ready := registry.VerifyQualificationArtifact(artifact, nil, now); ready.Ready {
					t.Fatal("v6 admitted")
				}
				artifact.Version = QualificationArtifactVersion
				backend.Capabilities.RestorePreflight = false
				registry.backends[backend.ID] = backend
				if ready := registry.VerifyQualificationArtifact(artifact, nil, now); ready.Ready {
					t.Fatal("capability mismatch admitted")
				}
				report.Recovery.InvalidPointRejected = false
				if ValidateQualificationReport(report) == nil {
					t.Fatal("missing rejection proof admitted")
				}
			} else if err == nil || ValidateQualificationReport(report) == nil || base.restore != 0 {
				t.Fatal("unproved source restored", report, err, base.restore)
			}
			if base.provision > 0 && !base.deleted {
				t.Fatal("source leaked")
			}
		})
	}
}
