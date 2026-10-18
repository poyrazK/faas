package realtime

// adr: 20261018074719

import (
	"os"
	"path/filepath"
	"testing"
)

// The generation that adopts a hand-off's old outbox delivers its callbacks
// in per-connection order after the ones it already queued for the same
// connection, keeps its dead letters, and leaves the old outbox empty.
func TestCallbackOutboxAbsorbMovesPendingAndDeadCallbacks(t *testing.T) {
	oldRoot := filepath.Join(t.TempDir(), "old")
	old := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: oldRoot, MaxAttempts: 1})
	dead := testCallbackEvent()
	dead.ID, dead.ConnectionID, dead.Sequence = "evt_dead", "connection-dead", 1
	if claimed, err := old.EnqueueAndClaim(dead); err != nil || !claimed {
		t.Fatalf("enqueue dead = (%v, %v)", claimed, err)
	}
	if err := old.Fail(dead.ID); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"evt_old_2", "evt_old_3"} {
		event := testCallbackEvent()
		event.ID, event.Sequence = id, uint64(i+2)
		if _, err := old.EnqueueAndClaim(event); err != nil {
			t.Fatal(err)
		}
		old.Release(event.ID)
	}

	next := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	first := testCallbackEvent()
	first.ID, first.Sequence = "evt_new_1", 1
	if _, err := next.EnqueueAndClaim(first); err != nil {
		t.Fatal(err)
	}
	next.Release(first.ID)

	moved, err := next.Absorb(oldRoot)
	if err != nil || moved != 2 {
		t.Fatalf("Absorb = (%d, %v), want 2 callbacks", moved, err)
	}
	if again, err := next.Absorb(oldRoot); err != nil || again != 0 {
		t.Fatalf("second Absorb = (%d, %v)", again, err)
	}
	for _, want := range []string{"evt_new_1", "evt_old_2", "evt_old_3"} {
		event, ok, err := next.ClaimNext()
		if err != nil || !ok || event.ID != want {
			t.Fatalf("ClaimNext = (%s, %v, %v), want %s", event.ID, ok, err, want)
		}
		if event.CallbackAuthToken != "callback-secret" {
			t.Fatalf("adopted callback lost its private fields: %+v", event)
		}
		if err := next.Ack(event.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := next.deadIDs[dead.ID]; !ok {
		t.Fatal("dead letter not adopted")
	}
	if _, err := os.Stat(filepath.Join(next.deadRoot, dead.ID+".json")); err != nil {
		t.Fatalf("adopted dead letter not on disk: %v", err)
	}
	entries, err := os.ReadDir(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Fatalf("old outbox still holds %s", entry.Name())
		}
	}
	// Durable across a restart of the adopting generation.
	reopened := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: next.root})
	if len(reopened.items) != 0 || len(reopened.deadIDs) != 1 {
		t.Fatalf("reopened outbox: %d pending, %d dead; want 0 and 1", len(reopened.items), len(reopened.deadIDs))
	}
}
