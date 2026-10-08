package runtimequota

// adr: 773
import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRuntimeConsentCanonicalScope(t *testing.T) {
	c := Consent{ID: uuid.NewString(), IssuerID: uuid.NewString(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(),
		Binding: Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "consent-app", APIBase: "https://fsn.example.com"}, ExpiresAtMicros: time.Now().Add(time.Hour).UnixMicro()}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeConsent(raw)
	if err != nil || decoded != c {
		t.Fatal("original scope changed", err)
	}
	for _, bad := range []string{string(raw) + "\n", strings.Replace(string(raw), `"issuer_id":`, `"unknown":`, 1), strings.Replace(string(raw), `"id":`, `"id":"`+uuid.NewString()+`","id":`, 1)} {
		if _, err := DecodeConsent([]byte(bad)); err == nil {
			t.Fatal("ambiguous consent accepted")
		}
	}
	c.Binding.RegionalOrgID = uuid.NewString()
	if c.Validate() == nil {
		t.Fatal("shared consent accepted by personal protocol")
	}
}

func TestDeliveryLegacySpellingSurvivesConsentExtension(t *testing.T) {
	d := Delivery{Binding: Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "old-wire", APIBase: "https://fsn.example.com"}}
	old, err := json.Marshal(struct {
		Binding   Binding  `json:"binding"`
		Authority Envelope `json:"authority"`
	}{d.Binding, d.Authority})
	if err != nil {
		t.Fatal(err)
	}
	now, err := json.Marshal(d)
	if err != nil || string(old) != string(now) {
		t.Fatal("legacy delivery bytes changed", err)
	}
}
