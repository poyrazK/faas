package runtimequota

// adr: 784
import (
	"crypto/ed25519"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func ingressFixture(t *testing.T) (IngressSnapshot, Signer, Trust) {
	t.Helper()
	g, directory, _, trust := fixture(t)
	return IngressSnapshot{DirectoryID: g.IssuerID, Generation: 1, IssuedAtMicros: g.IssuedAtMicros, ExpiresAtMicros: g.IssuedAtMicros + api.GlobalIngressLeaseDuration.Microseconds(), RoutingPolicy: api.GlobalRoutingPolicyAllSelected, Routes: []IngressRoute{{GlobalAppID: g.GlobalAppID, Targets: []IngressTarget{{Binding: g.Binding, Origin: "https://quota-regional.fsn.example.com", Healthy: true, Eligible: true}}}}}, directory, trust
}

func TestIngressSignedProjectionAndQuotaIsolation(t *testing.T) {
	s, signer, trust := ingressFixture(t)
	e, err := signer.SignIngressSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeIngressSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := trust.VerifyIngressSnapshot(decoded, s.DirectoryID)
	if err != nil || !reflect.DeepEqual(s, got) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if _, err := trust.VerifyGrant(e, s.Routes[0].Targets[0].Binding); err == nil {
		t.Fatal("route projection became a runtime grant")
	}
	if _, err := trust.VerifyIngressSnapshot(e, uuid.NewString()); err == nil {
		t.Fatal("foreign directory accepted")
	}
}

func TestIngressRejectsCellAuthorityAndTamper(t *testing.T) {
	s, signer, directoryTrust := ingressFixture(t)
	e, err := signer.SignIngressSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := NewTrust([]Anchor{{Role: Cell, IssuerID: s.DirectoryID, Public: signer.private.Public().(ed25519.PublicKey)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trust.VerifyIngressSnapshot(e, s.DirectoryID); err == nil {
		t.Fatal("cell key published directory routes")
	}
	e.Payload += "A"
	if _, err := directoryTrust.VerifyIngressSnapshot(e, s.DirectoryID); err == nil {
		t.Fatal("tampered projection accepted")
	}
}

func TestIngressProjectionValidation(t *testing.T) {
	original, signer, _ := ingressFixture(t)
	tests := []struct {
		name   string
		change func(*IngressSnapshot)
	}{
		{"foreign issuer", func(s *IngressSnapshot) { s.DirectoryID = uuid.NewString() }},
		{"generation zero", func(s *IngressSnapshot) { s.Generation = 0 }},
		{"generation overflow", func(s *IngressSnapshot) { s.Generation = api.GlobalRuntimeQuotaGenerationMax + 1 }},
		{"unbounded lease", func(s *IngressSnapshot) { s.ExpiresAtMicros++ }},
		{"reversed lease", func(s *IngressSnapshot) { s.ExpiresAtMicros = s.IssuedAtMicros }},
		{"primary policy", func(s *IngressSnapshot) { s.RoutingPolicy = "primary_region" }},
		{"duplicate app", func(s *IngressSnapshot) { s.Routes = append(s.Routes, s.Routes[0]) }},
		{"duplicate selected region", func(s *IngressSnapshot) {
			x := s.Routes[0].Targets[0]
			x.Binding.CellID = uuid.NewString()
			x.Binding.RegionalAppID = uuid.NewString()
			s.Routes[0].Targets = append(s.Routes[0].Targets, x)
		}},
		{"regional app reused", func(s *IngressSnapshot) {
			x := s.Routes[0]
			x.GlobalAppID = uuid.NewString()
			s.Routes = append(s.Routes, x)
		}},
		{"http origin", func(s *IngressSnapshot) { s.Routes[0].Targets[0].Origin = "http://app.example.com" }},
		{"origin credentials", func(s *IngressSnapshot) { s.Routes[0].Targets[0].Origin = "https://user:secret@app.example.com" }},
		{"origin path", func(s *IngressSnapshot) { s.Routes[0].Targets[0].Origin = "https://app.example.com/path" }},
		{"invalid binding", func(s *IngressSnapshot) { s.Routes[0].Targets[0].Binding.CellGeneration = 0 }},
		{"catalog bound", func(s *IngressSnapshot) { s.Routes = make([]IngressRoute, api.GlobalIngressRoutesMax+1) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			var s IngressSnapshot
			if err = json.Unmarshal(raw, &s); err != nil {
				t.Fatal(err)
			}
			tc.change(&s)
			if _, err := signer.SignIngressSnapshot(s); err == nil {
				t.Fatal("invalid projection signed")
			}
		})
	}
}

func TestIngressLargerCatalogDoesNotWidenLegacyQuotaBounds(t *testing.T) {
	s, signer, trust := ingressFixture(t)
	for i := 0; i < 32; i++ {
		r := s.Routes[0]
		r.GlobalAppID = uuid.NewString()
		r.Targets = append([]IngressTarget(nil), r.Targets...)
		r.Targets[0].Binding.RegionalAppID = uuid.NewString()
		s.Routes = append(s.Routes, r)
	}
	e, err := signer.SignIngressSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= api.RegionalRuntimeQuotaEnvelopeMaxBytes {
		t.Fatal("fixture did not exceed legacy envelope bound")
	}
	if _, err := Decode(raw); err == nil {
		t.Fatal("legacy quota envelope bound widened")
	}
	if _, err := DecodeIngressSnapshot(raw); err != nil {
		t.Fatal(err)
	}
	if _, err := trust.VerifyIngressSnapshot(e, s.DirectoryID); err != nil {
		t.Fatal(err)
	}
	if _, err := trust.VerifyGrant(e, s.Routes[0].Targets[0].Binding); err == nil {
		t.Fatal("large ingress envelope accepted as grant")
	}
	s.Routes = nil
	if _, err := signer.SignIngressSnapshot(s); err != nil {
		t.Fatal("empty withdrawal denied:", err)
	}
}
