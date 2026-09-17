package events

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// adr: 180
func TestEnvelopeNormalizeProducesCanonicalTenantScopedJSON(t *testing.T) {
	accountID := "00000000-0000-0000-0000-000000000001"
	when := time.Date(2026, 9, 17, 10, 11, 12, 13, time.FixedZone("test", 2*60*60))
	envelope, err := (Envelope{
		ID:     "evt-1",
		Source: "billing.service",
		Type:   "invoice.paid",
		Data:   json.RawMessage(`{"amount":150}`),
	}).Normalize(accountID, when)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if envelope.SpecVersion != CloudEventsSpecVersion || envelope.DataContentType != JSONDataContentType {
		t.Fatalf("defaults = %+v", envelope)
	}
	if envelope.AccountID != accountID || !envelope.Time.Equal(when.UTC()) {
		t.Fatalf("normalized ownership/time = %+v", envelope)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, field := range []string{"\"specversion\":\"1.0\"", "\"data_content_type\":\"application/json\"", "\"account_id\":\"" + accountID + "\""} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("encoded envelope missing %s: %s", field, encoded)
		}
	}
}

// adr: 180
func TestEnvelopeNormalizeRejectsCrossAccountAndInvalidData(t *testing.T) {
	_, err := (Envelope{
		ID:        "evt-1",
		Source:    "billing.service",
		Type:      "invoice.paid",
		Data:      json.RawMessage(`{}`),
		AccountID: "00000000-0000-0000-0000-000000000002",
	}).Normalize("00000000-0000-0000-0000-000000000001", time.Now())
	if err == nil || !strings.Contains(err.Error(), "account_id") {
		t.Fatalf("cross-account Normalize error = %v", err)
	}

	_, err = (Envelope{
		ID:     "evt-1",
		Source: "billing.service",
		Type:   "invoice.paid",
		Data:   json.RawMessage(`not-json`),
	}).Normalize("00000000-0000-0000-0000-000000000001", time.Now())
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("invalid data Normalize error = %v", err)
	}
}
