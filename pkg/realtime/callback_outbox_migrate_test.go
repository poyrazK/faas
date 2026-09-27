package realtime

// adr: 285

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateCallbackOutboxPreservesPendingOrderAndDeadLetters(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "legacy")
	persistent := filepath.Join(t.TempDir(), "persistent")
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: legacy, MaxAttempts: 1})
	first := testCallbackEvent()
	first.ID, first.Sequence = "evt_first", 1
	disconnect := testCallbackEvent()
	disconnect.ID, disconnect.Type, disconnect.Sequence = "evt_disconnect", EventDisconnect, 1
	dead := testCallbackEvent()
	dead.ID, dead.ConnectionID = "evt_dead", "other-connection"
	if claimed, err := queue.EnqueueAndClaim(first); err != nil || !claimed {
		t.Fatalf("first claim = (%v, %v)", claimed, err)
	}
	queue.Release(first.ID)
	if claimed, err := queue.EnqueueAndClaim(disconnect); err != nil || claimed {
		t.Fatalf("disconnect claim = (%v, %v), want queued behind message", claimed, err)
	}
	if claimed, err := queue.EnqueueAndClaim(dead); err != nil || !claimed {
		t.Fatalf("dead-letter claim = (%v, %v)", claimed, err)
	}
	if err := queue.Fail(dead.ID); err != nil {
		t.Fatal(err)
	}

	// Simulate an interrupted migration: the destination copy was persisted,
	// but the old file had not yet been removed.
	if err := os.MkdirAll(persistent, 0o700); err != nil {
		t.Fatal(err)
	}
	firstPayload, err := os.ReadFile(filepath.Join(legacy, first.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCallbackOutboxFile(filepath.Join(persistent, first.ID+".json"), firstPayload); err != nil {
		t.Fatal(err)
	}
	if err := MigrateCallbackOutbox(legacy, persistent); err != nil {
		t.Fatal(err)
	}
	if err := MigrateCallbackOutbox(legacy, persistent); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	for _, name := range []string{first.ID + ".json", disconnect.ID + ".json", filepath.Join("dead", dead.ID+".json")} {
		if _, err := os.Stat(filepath.Join(legacy, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy event %s remains: %v", name, err)
		}
	}
	info, err := os.Stat(filepath.Join(persistent, first.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("persistent callback permissions = (%v, %v)", info, err)
	}
	restarted := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: persistent})
	if stats := restarted.Stats(); stats.Pending != 2 || stats.DeadLetterTotal != 1 {
		t.Fatalf("migrated stats = %+v", stats)
	}
	for _, want := range []string{first.ID, disconnect.ID} {
		event, ok, err := restarted.ClaimNext()
		if err != nil || !ok || event.ID != want {
			t.Fatalf("migrated replay = (%+v, %v, %v), want %s", event, ok, err, want)
		}
		if err := restarted.Ack(event.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrateCallbackOutboxRejectsConflictingEvent(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "legacy")
	persistent := filepath.Join(t.TempDir(), "persistent")
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: legacy})
	event := testCallbackEvent()
	if _, err := queue.EnqueueAndClaim(event); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(persistent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(persistent, event.ID+".json"), []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateCallbackOutbox(legacy, persistent); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting event migration = %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, event.ID+".json")); err != nil {
		t.Fatalf("conflict removed legacy event: %v", err)
	}
}
