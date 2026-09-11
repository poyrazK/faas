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
