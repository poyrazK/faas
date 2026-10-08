// adr: 650 — advertising Data API support requires live credential evidence.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
)

type qualificationDataAPI struct{ *qualificationProvider }

func (*qualificationDataAPI) ProbeDataAPICredentials(context.Context, string) (DataAPICredentialEvidence, error) {
	return DataAPICredentialEvidence{SchemaIsolated: true, RLSEnforced: true, PasswordRecovered: true, RotationPreservesData: true, Revoked: true}, nil
}

func TestQualificationDataAPIRequiresLiveEvidence(t *testing.T) {
	p := &qualificationProvider{capabilities: testCapabilities()}
	p.capabilities.CredentialAccess = append(p.capabilities.CredentialAccess, CredentialDataAPI)
	_, err := QualifyProvider(context.Background(), p, QualificationOptions{ProviderName: "fake", ResourceID: "api-contract", Spec: testSpec(), Mutating: true})
	if !errors.Is(err, ErrQualificationFailed) {
		t.Fatal("unproven data API qualified", err)
	}
	p = &qualificationProvider{capabilities: p.capabilities}
	r, err := QualifyProvider(context.Background(), &qualificationDataAPI{p}, QualificationOptions{ProviderName: "fake", ResourceID: "api-contract", Spec: testSpec(), Mutating: true})
	if err != nil || ValidateQualificationReport(r) != nil {
		t.Fatal("complete evidence rejected", err)
	}
	r.DataAPICredentials.RLSEnforced = false
	if ValidateQualificationReport(r) == nil {
		t.Fatal("missing RLS proof accepted")
	}
	r.DataAPICredentials = nil
	if ValidateQualificationReport(r) == nil {
		t.Fatal("missing evidence accepted")
	}
}

func TestDataAPIRolloutDoesNotRepurposePinnedDatabasePlacement(t *testing.T) {
	config := BackendConfig{Driver: "neon", Region: "eu-central-1", Namespace: "org-example", Settings: map[string]string{"region_id": "aws-eu-central-1"}}
	before := fingerprint(config)
	config.DataAPIEnabled = true
	if fingerprint(config) != before {
		t.Fatal("enabling a credential contract invalidated existing database placement")
	}
}
