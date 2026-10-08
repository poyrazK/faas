package runtimequota

// adr: 658
import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
)

func TestServingProbePurposeAndChallenge(t *testing.T) {
	grant, signer, _, trust := fixture(t)
	p := ServingProbe{Nonce: uuid.NewString(), ConsentID: uuid.NewString(), Grant: grant, Origin: "https://quota-regional.apps.example.com", IssuedAtMicros: grant.IssuedAtMicros, ExpiresAtMicros: grant.IssuedAtMicros + api.GlobalIngressLeaseDuration.Microseconds()}
	e, err := signer.SignServingProbe(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := trust.VerifyServingProbe(e)
	if err != nil || got != p {
		t.Fatal("roundtrip", err)
	}
	if _, err := trust.VerifyGrant(e, grant.Binding); err == nil {
		t.Fatal("probe became runtime grant")
	}
	if _, err := trust.VerifyIngressSnapshot(e, grant.IssuerID); err == nil {
		t.Fatal("probe became traffic projection")
	}
	s := ServingStatus{Probe: p, ObservedAtMicros: p.IssuedAtMicros + 1, ExpiresAtMicros: p.ExpiresAtMicros, Healthy: true, Eligible: true, Code: "ready"}
	raw, _ := json.Marshal(s)
	if _, err := DecodeServingStatus(raw, p); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ServingStatus){func(s *ServingStatus) { s.Probe.Nonce = uuid.NewString() }, func(s *ServingStatus) { s.ObservedAtMicros--; s.ObservedAtMicros-- }, func(s *ServingStatus) { s.ExpiresAtMicros++ }, func(s *ServingStatus) { s.Code = "admission_frozen" }} {
		bad := s
		change(&bad)
		raw, _ := json.Marshal(bad)
		if _, err := DecodeServingStatus(raw, p); err == nil {
			t.Fatal("mismatched status accepted")
		}
	}
	if _, err := DecodeServingStatus(append(raw, '\n'), p); err == nil {
		t.Fatal("noncanonical response accepted")
	}
}
