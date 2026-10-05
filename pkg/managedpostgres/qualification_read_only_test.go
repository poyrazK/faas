// adr: 592 — portable reader permissions and capability discovery.
package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestQualificationRequiresReadOnlyEvidenceForAdvertisedSupport(t *testing.T) {
	for name, mutate := range map[string]func(*QualificationReport){
		"missing evidence":   func(r *QualificationReport) { r.ReadOnlyCredentials = nil },
		"writable reader":    func(r *QualificationReport) { r.ReadOnlyCredentials.Restricted = false },
		"password changed":   func(r *QualificationReport) { r.ReadOnlyCredentials.PasswordRecovered = false },
		"rotation lost data": func(r *QualificationReport) { r.ReadOnlyCredentials.RotationPreservesData = false },
		"revocation failed":  func(r *QualificationReport) { r.ReadOnlyCredentials.Revoked = false },
		"missing check": func(r *QualificationReport) {
			for i, c := range r.Checks {
				if c.Name == "read_only_credentials_probe" {
					r.Checks = append(r.Checks[:i], r.Checks[i+1:]...)
					break
				}
			}
		},
		"empty capabilities": func(r *QualificationReport) { r.CredentialAccess = nil },
		"duplicate capabilities": func(r *QualificationReport) {
			r.CredentialAccess = []CredentialAccess{CredentialReadOnly, CredentialReadOnly}
		},
		"unknown capability": func(r *QualificationReport) { r.CredentialAccess = []CredentialAccess{"provider_specific"} },
	} {
		t.Run(name, func(t *testing.T) {
			p := &qualificationProvider{capabilities: testCapabilities()}
			r, err := QualifyProvider(context.Background(), p, QualificationOptions{ProviderName: "fixture", ResourceID: "reader-contract", Spec: testSpec(), Mutating: true})
			if err != nil || ValidateQualificationReport(r) != nil {
				t.Fatal("baseline qualification failed", err)
			}
			mutate(&r)
			if ValidateQualificationReport(r) == nil {
				t.Fatal("incomplete credential contract accepted")
			}
		})
	}
}

// Hide the optional reader probe while preserving the other qualification hooks.
type qualificationWithoutReader struct {
	Provider
	CredentialPrivilegeProber
	ScaleToZeroProber
	RestoreDataProber
	RestoreCredentialIsolationProber
}

func TestQualificationReadOnlyProbeIsRequiredOnlyWhenAdvertised(t *testing.T) {
	for _, advertised := range []bool{false, true} {
		p := &qualificationProvider{capabilities: testCapabilities()}
		if !advertised {
			p.capabilities.CredentialAccess = []CredentialAccess{CredentialReadWrite}
		}
		provider := qualificationWithoutReader{p, p, p, p, p}
		r, err := QualifyProvider(context.Background(), provider, QualificationOptions{ProviderName: "fixture", ResourceID: "reader-contract", Spec: testSpec(), Mutating: true})
		if advertised {
			if !errors.Is(err, ErrQualificationFailed) || !p.deleted {
				t.Fatal("unsupported reader probe accepted or cleanup skipped", err)
			}
		} else {
			if err != nil || r.ReadOnlyCredentials != nil || ValidateQualificationReport(r) != nil {
				t.Fatal("provider without reader support must qualify its smaller contract", err)
			}
			r.ReadOnlyCredentials = &ReadOnlyCredentialEvidence{Restricted: true, PasswordRecovered: true, RotationPreservesData: true, Revoked: true}
			if ValidateQualificationReport(r) == nil {
				t.Fatal("unadvertised reader evidence accepted")
			}
		}
	}
}

func TestQualificationRejectsVersionThreeAndChangedCredentialCapabilities(t *testing.T) {
	p := &qualificationProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, p, nil)
	b, err := registry.Default(registry.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	r, err := QualifyProvider(context.Background(), p, QualificationOptions{ProviderName: b.Driver, ResourceID: "reader-contract", Spec: testSpec(), Mutating: true})
	if err != nil {
		t.Fatal(err)
	}
	now := r.CompletedAt.Add(time.Minute)
	lifecycle := passingLifecycleQualificationReport()
	approval, err := BuildQualificationApproval(r, &lifecycle, b.ID, b.Fingerprint, nil, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := QualificationArtifact{Version: QualificationArtifactVersion, BackendID: b.ID, BackendFingerprint: b.Fingerprint, Spec: testSpec(), Report: r, Lifecycle: &lifecycle, Approval: &approval}
	if result := registry.VerifyQualificationArtifact(a, nil, now); !result.Ready {
		t.Fatalf("current contract rejected: %+v", result)
	}
	old := a
	old.Version = 3
	if result := registry.VerifyQualificationArtifact(old, nil, now); result.Ready || !strings.Contains(strings.Join(result.Reasons, ","), "artifact_version_invalid") {
		t.Fatalf("old contract accepted: %+v", result)
	}
	b.Capabilities.CredentialAccess = []CredentialAccess{CredentialReadWrite}
	registry.backends[b.ID] = b
	if result := registry.VerifyQualificationArtifact(a, nil, now); result.Ready || !strings.Contains(strings.Join(result.Reasons, ","), "credential_capabilities_mismatch") {
		t.Fatalf("changed capability declaration accepted: %+v", result)
	}
}
