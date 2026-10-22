package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1322 — the trigger-ledger reaper is an exact opt-in until trigger
// ledgers have been tested end to end.
func TestTriggersRetentionIsExactOptIn(t *testing.T) {
	store := state.NewMemStore()
	for value, want := range map[string]bool{"1": true, " 1 ": true, "": false, "0": false, "true": false} {
		if got := triggersRetention(store, nil, value) != nil; got != want {
			t.Errorf("triggersRetention(%q) attached = %v, want %v", value, got, want)
		}
	}
}
