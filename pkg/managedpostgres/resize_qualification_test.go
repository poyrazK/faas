// adr: 593 — provider capability claims require data and credential proof.
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
