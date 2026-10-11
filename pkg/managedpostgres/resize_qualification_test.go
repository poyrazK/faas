// adr: 623 — provider capability claims require data and credential proof.
package managedpostgres

import (
	"context"
	"testing"
	"time"
)

type resizeQualificationProvider struct {
	*qualificationProvider
	evidence ResizeEvidence
	probeErr error
}

func (p *resizeQualificationProvider) ProbeComputeResize(context.Context, string, Spec) (ResizeEvidence, error) {
	return p.evidence, p.probeErr
}
func TestResizeQualificationRequiresCompleteProof(t *testing.T) {
	for _, kind := range []string{"complete", "missing identity", "missing data", "missing credentials", "missing replay", "missing restoration", "provider error", "missing prober"} {
		t.Run(kind, func(t *testing.T) {
			caps := testCapabilities()
			caps.ClassResize = true
			base := &qualificationProvider{capabilities: caps}
			proof := ResizeEvidence{IdentityPreserved: true, DataPreserved: true, CredentialsPreserved: true, ReplayStable: true, OriginalClassRestored: true}
			p := &resizeQualificationProvider{qualificationProvider: base, evidence: proof}
			var provider Provider = p
			switch kind {
			case "missing identity":
				p.evidence.IdentityPreserved = false
			case "missing data":
				p.evidence.DataPreserved = false
			case "missing credentials":
				p.evidence.CredentialsPreserved = false
			case "missing replay":
				p.evidence.ReplayStable = false
			case "missing restoration":
				p.evidence.OriginalClassRestored = false
			case "provider error":
				p.probeErr = ErrUnavailable
			case "missing prober":
				provider = base
			}
			report, err := QualifyProvider(t.Context(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "resize-qualification", Spec: testSpec(), Mutating: true, Timeout: time.Minute})
			if kind == "complete" {
				if err != nil || ValidateQualificationReport(report) != nil || !report.ClassResize || report.Resize == nil {
					t.Fatal("proof rejected", report, err)
				}
			} else if err == nil || ValidateQualificationReport(report) == nil {
				t.Fatal("unproved capability admitted", report, err)
			}
			if !base.deleted {
				t.Fatal("qualification leaked disposable resource")
			}
		})
	}
}

func TestResizeQualificationBindsCapabilityAndVersion(t *testing.T) {
	caps := testCapabilities()
	caps.ClassResize = true
	provider := &resizeQualificationProvider{qualificationProvider: &qualificationProvider{capabilities: caps}, evidence: ResizeEvidence{IdentityPreserved: true, DataPreserved: true, CredentialsPreserved: true, ReplayStable: true, OriginalClassRestored: true}}
	registry := testRegistry(t, provider, nil)
	backend, err := registry.Default(registry.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	report, err := QualifyProvider(t.Context(), provider, QualificationOptions{ProviderName: backend.Driver, ResourceID: "resize-approval", Spec: testSpec(), Mutating: true})
	if err != nil {
		t.Fatal(err)
	}
	now := report.CompletedAt.Add(time.Minute)
	lifecycle := passingLifecycleQualificationReport()
	approval, err := BuildQualificationApproval(report, &lifecycle, backend.ID, backend.Fingerprint, nil, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	artifact := QualificationArtifact{Version: QualificationArtifactVersion, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Spec: testSpec(), Report: report, Lifecycle: &lifecycle, Approval: &approval}
	if ready := registry.VerifyQualificationArtifact(artifact, nil, now); !ready.Ready {
		t.Fatal("complete resize approval rejected", ready)
	}
	backend.Capabilities.ClassResize = false
	registry.backends[backend.ID] = backend
	ready := registry.VerifyQualificationArtifact(artifact, nil, now)
	found := false
	for _, reason := range ready.Reasons {
		if reason == "resize_capabilities_mismatch" {
			found = true
		}
	}
	if ready.Ready || !found {
		t.Fatal("different provider declaration accepted", ready)
	}
	backend.Capabilities.ClassResize = true
	registry.backends[backend.ID] = backend
	artifact.Version = 4
	if ready := registry.VerifyQualificationArtifact(artifact, nil, now); ready.Ready {
		t.Fatal("prior approval contract accepted")
	}
}
