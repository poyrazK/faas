// adr: 624 — provider capability claims require data and credential proof.
package managedpostgres

import (
	"context"
	"testing"
	"time"
)

type policyQualificationProvider struct {
	*qualificationProvider
	evidence ComputePolicyEvidence
	probeErr error
}

func (p *policyQualificationProvider) ProbeComputePolicy(context.Context, string, Spec) (ComputePolicyEvidence, error) {
	return p.evidence, p.probeErr
}
func TestComputePolicyQualificationRequiresCompleteProof(t *testing.T) {
	for _, kind := range []string{"complete", "missing identity", "missing data", "missing credentials", "missing replay", "missing restoration", "missing always on", "missing suspend", "missing resume", "provider error", "missing prober"} {
		t.Run(kind, func(t *testing.T) {
			caps := testCapabilities()
			caps.ScaleToZeroUpdate = true
			base := &qualificationProvider{capabilities: caps}
			proof := ComputePolicyEvidence{IdentityPreserved: true, DataPreserved: true, CredentialsPreserved: true, ReplayStable: true, OriginalPolicyRestored: true, AlwaysOnObserved: true, Suspended: true, Resumed: true}
			p := &policyQualificationProvider{qualificationProvider: base, evidence: proof}
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
				p.evidence.OriginalPolicyRestored = false
			case "missing always on":
				p.evidence.AlwaysOnObserved = false
			case "missing suspend":
				p.evidence.Suspended = false
			case "missing resume":
				p.evidence.Resumed = false
			case "provider error":
				p.probeErr = ErrUnavailable
			case "missing prober":
				provider = base
			}
			report, err := QualifyProvider(t.Context(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "policy-qualification", Spec: testSpec(), Mutating: true, Timeout: time.Minute})
			if kind == "complete" {
				if err != nil || ValidateQualificationReport(report) != nil || !report.ScaleToZeroUpdate || report.ComputePolicy == nil {
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

func TestComputePolicyQualificationBindsCapabilityAndVersion(t *testing.T) {
	caps := testCapabilities()
	caps.ScaleToZeroUpdate = true
	provider := &policyQualificationProvider{qualificationProvider: &qualificationProvider{capabilities: caps}, evidence: ComputePolicyEvidence{IdentityPreserved: true, DataPreserved: true, CredentialsPreserved: true, ReplayStable: true, OriginalPolicyRestored: true, AlwaysOnObserved: true, Suspended: true, Resumed: true}}
	registry := testRegistry(t, provider, nil)
	backend, err := registry.Default(registry.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	report, err := QualifyProvider(t.Context(), provider, QualificationOptions{ProviderName: backend.Driver, ResourceID: "policy-approval", Spec: testSpec(), Mutating: true})
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
		t.Fatal("complete policy approval rejected", ready)
	}
	backend.Capabilities.ScaleToZeroUpdate = false
	registry.backends[backend.ID] = backend
	ready := registry.VerifyQualificationArtifact(artifact, nil, now)
	found := false
	for _, reason := range ready.Reasons {
		if reason == "compute_policy_capabilities_mismatch" {
			found = true
		}
	}
	if ready.Ready || !found {
		t.Fatal("different provider declaration accepted", ready)
	}
	backend.Capabilities.ScaleToZeroUpdate = true
	registry.backends[backend.ID] = backend
	artifact.Version = 5
	if ready := registry.VerifyQualificationArtifact(artifact, nil, now); ready.Ready {
		t.Fatal("prior approval contract accepted")
	}
}
