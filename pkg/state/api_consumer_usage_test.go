package state

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The idempotency and anonymous-isolation checks cover the durable usage
// contract described by ADR-120 and the customer monetization ledger target.
func TestMemStoreAPIConsumerUsageIsIdempotent(t *testing.T) {
	store := NewMemStore()
	now := time.Date(2026, 9, 11, 12, 34, 0, 0, time.UTC)
	event := APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(),
		ConsumerKey: uuid.NewString(), WindowStart: now,
		RequestCount: 3, ErrorCount: 1, BillableUnits: 3,
	}
	applied, err := store.RecordAPIConsumerUsage(context.Background(), event)
	if err != nil || !applied {
		t.Fatalf("first usage event = applied %v, err %v; want applied", applied, err)
	}
	applied, err = store.RecordAPIConsumerUsage(context.Background(), event)
	if err != nil || applied {
		t.Fatalf("retry usage event = applied %v, err %v; want duplicate ignored", applied, err)
	}
	rows, err := store.ListAPIConsumerUsage(context.Background(), event.AccountID, event.AppID, event.ConsumerKey, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ListAPIConsumerUsage: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestCount != 3 || rows[0].ErrorCount != 1 || rows[0].BillableUnits != 3 {
		t.Fatalf("usage rows = %#v, want one 3/1/3 bucket", rows)
	}
}

func TestMemStoreAPIConsumerUsageKeepsAnonymousSeparate(t *testing.T) {
	store := NewMemStore()
	now := time.Date(2026, 9, 11, 12, 34, 0, 0, time.UTC)
	base := APIConsumerUsageEvent{
		AccountID: uuid.NewString(), AppID: uuid.NewString(), WindowStart: now,
		RequestCount: 1, BillableUnits: 1,
	}
	for _, key := range []string{AnonymousConsumerKey, uuid.NewString()} {
		event := base
		event.EventID = uuid.NewString()
		event.ConsumerKey = key
		if _, err := store.RecordAPIConsumerUsage(context.Background(), event); err != nil {
			t.Fatalf("RecordAPIConsumerUsage(%q): %v", key, err)
		}
	}
	rows, err := store.ListAPIConsumerUsage(context.Background(), base.AccountID, base.AppID, AnonymousConsumerKey, now, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ListAPIConsumerUsage anonymous: %v", err)
	}
	if len(rows) != 1 || rows[0].ConsumerKey != AnonymousConsumerKey {
		t.Fatalf("anonymous rows = %#v", rows)
	}
}

func TestRequestAuditReplayFillsMissingEvidenceWithoutDoubleUsage(t *testing.T) {
	store := NewMemStore()
	now := time.Now().UTC().Truncate(time.Minute)
	event := APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(),
		ConsumerKey: uuid.NewString(), WindowStart: now,
		RequestCount: 1, BillableUnits: 1,
	}
	if applied, err := store.RecordAPIConsumerUsage(context.Background(), event); err != nil || !applied {
		t.Fatalf("legacy usage: applied=%v err=%v", applied, err)
	}
	event.Audit = &RequestAuditEvidence{
		RouteTemplate: "POST /payments", Method: "POST", HTTPStatus: 201,
		LatencyMS: 381, OccurredAt: now.Add(13 * time.Second), RequestID: "req-1",
	}
	if applied, err := store.RecordAPIConsumerUsage(context.Background(), event); err != nil || applied {
		t.Fatalf("audit replay: applied=%v err=%v", applied, err)
	}
	if applied, err := store.RecordAPIConsumerUsage(context.Background(), event); err != nil || applied {
		t.Fatalf("duplicate replay: applied=%v err=%v", applied, err)
	}
	rows, err := store.ListRequestAudit(context.Background(), event.AccountID, event.AppID, now, now.Add(time.Minute), 100)
	if err != nil || len(rows) != 1 || rows[0].RouteTemplate != "POST /payments" {
		t.Fatalf("audit rows=%+v err=%v", rows, err)
	}
	usage, err := store.ListAPIConsumerUsage(context.Background(), event.AccountID, event.AppID, event.ConsumerKey, now, now.Add(time.Minute))
	if err != nil || len(usage) != 1 || usage[0].RequestCount != 1 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	routes, err := store.ListDiscoveredAuditRoutes(context.Background(), event.AccountID, event.AppID, 100)
	if err != nil || len(routes) != 1 || routes[0] != "POST /payments" {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	other, err := store.ListRequestAudit(context.Background(), uuid.NewString(), event.AppID, now, now.Add(time.Minute), 100)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-account audit=%+v err=%v", other, err)
	}
}

func TestRequestAuditRejectsMalformedSourceIP(t *testing.T) {
	now := time.Now().UTC()
	event := APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(),
		ConsumerKey: AnonymousConsumerKey, WindowStart: now.Truncate(time.Minute),
		RequestCount: 1, BillableUnits: 1,
		Audit: &RequestAuditEvidence{RouteTemplate: "GET /profile/{id}", Method: "GET", HTTPStatus: 200,
			OccurredAt: now, SourceIP: "spoofed.example"},
	}
	if err := ValidateAPIConsumerUsageEvent(event); err == nil {
		t.Fatal("malformed source IP accepted")
	}
}

func TestRequestAuditRejectsNULRoute(t *testing.T) {
	now := time.Now().UTC()
	event := APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(),
		ConsumerKey: AnonymousConsumerKey, WindowStart: now.Truncate(time.Minute),
		RequestCount: 1, BillableUnits: 1,
		Audit: &RequestAuditEvidence{RouteTemplate: "GET /profile/name\x00other", Method: "GET", HTTPStatus: 200,
			OccurredAt: now},
	}
	if err := ValidateAPIConsumerUsageEvent(event); err == nil {
		t.Fatal("NUL-containing audit route accepted")
	}
}
