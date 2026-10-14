// adr: 1190 — advertising application Auth support requires live credential evidence.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
)

type qualificationAuth struct{ *qualificationProvider }

func (*qualificationAuth) ProbeAuthCredentials(context.Context, string) (AuthCredentialEvidence, error) {
	return AuthCredentialEvidence{SchemaOwned: true, CustomerIsolated: true, PasswordRecovered: true, RotationPreservesData: true, Revoked: true}, nil
}

func TestQualificationAuthRequiresLiveEvidence(t *testing.T) {
	p := &qualificationProvider{capabilities: testCapabilities()}
	p.capabilities.CredentialAccess = append(p.capabilities.CredentialAccess, CredentialAuth)
	_, err := QualifyProvider(context.Background(), p, QualificationOptions{ProviderName: "fake", ResourceID: "auth-contract", Spec: testSpec(), Mutating: true})
	if !errors.Is(err, ErrQualificationFailed) {
		t.Fatal("unproven auth qualified", err)
	}
	p = &qualificationProvider{capabilities: p.capabilities}
	r, err := QualifyProvider(context.Background(), &qualificationAuth{p}, QualificationOptions{ProviderName: "fake", ResourceID: "auth-contract", Spec: testSpec(), Mutating: true})
	if err != nil || ValidateQualificationReport(r) != nil {
		t.Fatal("complete evidence rejected", err)
	}
	r.AuthCredentials.CustomerIsolated = false
	if ValidateQualificationReport(r) == nil {
		t.Fatal("missing isolation proof accepted")
	}
	r.AuthCredentials = nil
	if ValidateQualificationReport(r) == nil {
		t.Fatal("missing evidence accepted")
	}
}

func TestQualificationAuthEvidenceRequiresAdvertisedMode(t *testing.T) {
	p := &qualificationProvider{capabilities: testCapabilities()}
	r, err := QualifyProvider(context.Background(), p, QualificationOptions{ProviderName: "fake", ResourceID: "auth-contract", Spec: testSpec(), Mutating: true})
	if err != nil {
		t.Fatal(err)
	}
	r.AuthCredentials = &AuthCredentialEvidence{SchemaOwned: true, CustomerIsolated: true, PasswordRecovered: true, RotationPreservesData: true, Revoked: true}
	if !errors.Is(ValidateQualificationReport(r), ErrInvalid) {
		t.Fatal("auth evidence accepted for a backend that does not advertise auth")
	}
}

func TestAuthRolloutDoesNotRepurposePinnedDatabasePlacement(t *testing.T) {
	config := BackendConfig{Driver: "neon", Region: "eu-central-1", Namespace: "org-example", Settings: map[string]string{"region_id": "aws-eu-central-1"}}
	before := fingerprint(config)
	config.AuthEnabled = true
	if fingerprint(config) != before {
		t.Fatal("enabling a credential contract invalidated existing database placement")
	}
}

func TestCredentialAccessKnownCoversEveryMode(t *testing.T) {
	for _, access := range []CredentialAccess{CredentialReadWrite, CredentialReadOnly, CredentialMigration, CredentialDataAPI, CredentialAuth} {
		if !access.Known() {
			t.Fatalf("%s rejected", access)
		}
	}
	for _, access := range []CredentialAccess{"", "admin", "AUTH", "owner"} {
		if access.Known() {
			t.Fatalf("%q accepted", access)
		}
	}
}
