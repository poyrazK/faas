package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// A publish batch is accepted in one call: each event gets its own outcome,
// an identity conflict refuses only that event, and a repeat in the same
// batch is a duplicate (2026-11-12 throughput test: one transaction per event
// held batches to ~15–30 events/s).
func TestAcceptPublishedEventBatchSettlesEachEvent(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := t.Context()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		event := func(id, typ string) []byte {
			payload, _ := json.Marshal(map[string]any{"specversion": "1.0", "id": id, "source": "orders", "type": typ,
				"accountid": canonicalMemUUID(account.ID), "datacontenttype": "application/json", "time": time.Now().UTC(), "data": map[string]any{"n": 1}})
			return payload
		}
		first, second := uuid.NewString(), uuid.NewString()
		batch := store.(PublishedEventBatchAcceptanceStore)
		got, err := batch.AcceptPublishedEventBatch(ctx, "apid", account.ID, [][]byte{
			event(first, "order.created"),
			event(second, "order.created"),
			event(first, "order.created"),    // same identity and content: duplicate
			event(second, "order.cancelled"), // same identity, other content: conflict
		})
		if err != nil || len(got) != 4 {
			t.Fatalf("AcceptPublishedEventBatch() = %+v, %v", got, err)
		}
		if got[0].Err != nil || got[0].Acceptance.Duplicate || got[1].Err != nil || got[1].Acceptance.Duplicate {
			t.Fatalf("new events = %+v, %+v; want accepted", got[0], got[1])
		}
		if got[2].Err != nil || !got[2].Acceptance.Duplicate {
			t.Fatalf("repeat = %+v; want duplicate", got[2])
		}
		if got[3].Err == nil {
			t.Fatalf("conflicting identity = %+v; want refused", got[3])
		}
		// The refused event left the others accepted.
		for _, id := range []string{first, second} {
			if _, err := store.(EventReceiptAcceptanceStore).EventReceiptAcceptedAt(ctx, account.ID, "orders", id); err != nil {
				t.Fatalf("event %s not accepted: %v", id, err)
			}
		}
	})
}
