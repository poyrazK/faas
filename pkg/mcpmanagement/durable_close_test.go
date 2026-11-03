package mcpmanagement

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// failsOnClose stands in for the Postgres store, whose close waits for a lease
// renewal that may record a lost lease through StateStore.fail.
type failsOnClose struct{ store *StateStore }

func (f *failsOnClose) load() (map[string]json.RawMessage, error) { return nil, nil }
func (f *failsOnClose) commit(map[string]json.RawMessage, []string) error {
	return nil
}
func (f *failsOnClose) close() error {
	f.store.fail(errors.New("lease lost while closing"))
	return nil
}

// Closing while the renewal reports a lost lease used to deadlock: Close held
// the store's mutex while it waited for the renewal, and the renewal waited
// for that mutex (TestPostgresStateLeaseFencesAFormerHolder hung on it).
func TestStateStoreCloseLetsALostLeaseBeRecorded(t *testing.T) {
	persist := &failsOnClose{}
	store, err := openStateStore(persist)
	if err != nil {
		t.Fatal(err)
	}
	persist.store = store
	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close deadlocked with a failure recorded while closing")
	}
	if store.available() {
		t.Fatal("a closed store reports itself available")
	}
	if err := store.update("plans/g", map[string]int{"n": 1}); err == nil {
		t.Fatal("a closed store accepted a write")
	}
}
