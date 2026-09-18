package events

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// adr: 181
func TestSubscriptionMatchPatternAndContentFilter(t *testing.T) {
	event := testEnvelope(t, accountA, "billing.eu", "invoice.paid", `{"amount":150,"invoice_id":"inv-123"}`)
	subscription := Subscription{
		ID:        "sub-1",
		AccountID: accountA,
		Source:    "billing.*",
		Type:      "invoice.paid",
		Filter:    json.RawMessage(`{"data":{"amount":{"$gt":100},"invoice_id":{"$suffix":"123"}}}`),
	}
	matched, err := subscription.Match(event)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !matched {
		t.Fatal("subscription did not match event")
	}
}

// adr: 181
func TestSubscriptionMatchSupportsExactAndNumericComparisons(t *testing.T) {
	event := testEnvelope(t, accountA, "billing", "invoice.paid", `{"amount":100}`)
	for _, test := range []struct {
		name   string
		filter string
		want   bool
	}{
		{"exact", `{"data":{"amount":100}}`, true},
		{"equal operator", `{"data":{"amount":{"$eq":100.0}}}`, true},
		{"greater than miss", `{"data":{"amount":{"$gt":100}}}`, false},
		{"less than", `{"data":{"amount":{"$lt":101}}}`, true},
		{"prefix miss", `{"data":{"missing":{"$prefix":"x"}}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			subscription := Subscription{AccountID: accountA, Source: "billing", Type: "invoice.paid", Filter: json.RawMessage(test.filter)}
			matched, err := subscription.Match(event)
			if err != nil {
				t.Fatalf("Match: %v", err)
			}
			if matched != test.want {
				t.Fatalf("matched = %v, want %v", matched, test.want)
			}
		})
	}
}

// adr: 181
func TestSubscriptionMatchEnforcesTenantIsolation(t *testing.T) {
	event := testEnvelope(t, accountA, "billing", "invoice.paid", `{"amount":150}`)
	subscription := Subscription{AccountID: accountB, Source: "*", Type: "*", Filter: json.RawMessage(`{"data":{"amount":{"$gt":0}}}`)}
	matched, err := subscription.Match(event)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if matched {
		t.Fatal("cross-account subscription matched event")
	}
}

// adr: 181
func TestSubscriptionMatchRejectsMalformedFilterAndInteriorWildcard(t *testing.T) {
	event := testEnvelope(t, accountA, "billing", "invoice.paid", `{"amount":150}`)
	badFilter := Subscription{AccountID: accountA, Source: "billing", Type: "invoice.paid", Filter: json.RawMessage(`{"data":{"amount":{"$wat":1}}}`)}
	if _, err := badFilter.Match(event); err == nil || !strings.Contains(err.Error(), "unsupported operator") {
		t.Fatalf("bad filter error = %v", err)
	}
	badPattern := Subscription{AccountID: accountA, Source: "billing.*.service", Type: "invoice.paid"}
	if _, err := badPattern.Match(event); err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("bad pattern error = %v", err)
	}
	trailingFilter := badFilter
	trailingFilter.Filter = json.RawMessage(`{"data":{}} {"data":{}}`)
	if _, err := trailingFilter.Match(event); err == nil || !strings.Contains(err.Error(), "one JSON value") {
		t.Fatalf("trailing filter error = %v", err)
	}
}

const (
	accountA = "00000000-0000-0000-0000-000000000001"
	accountB = "00000000-0000-0000-0000-000000000002"
)

func testEnvelope(t *testing.T, accountID, source, eventType, data string) Envelope {
	t.Helper()
	event, err := (Envelope{ID: "evt-1", Source: source, Type: eventType, Data: json.RawMessage(data)}).Normalize(accountID, time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return event
}
