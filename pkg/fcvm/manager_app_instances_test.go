package fcvm

import (
	"slices"
	"testing"
)

// TestManagerAppInstanceIDsListsOnlyOrdinaryAppVMs pins the set vmmd's
// live-VM reap may act on (H9-171): VMs that settle through their own
// lifecycle are never offered to it.
func TestManagerAppInstanceIDsListsOnlyOrdinaryAppVMs(t *testing.T) {
	m := &Manager{live: map[string]*Instance{
		"app":           {},
		"builder":       {Lease: Lease{IsBuilder: true}},
		"execution":     {ExecutionOnly: true},
		"app-task":      {AppTaskOnly: true},
		"qualification": {QualificationOnly: true},
		"job":           {IsJob: true},
		"warm":          {Paused: true},
		"nil":           nil,
	}}
	if got := m.AppInstanceIDs(); !slices.Equal(got, []string{"app"}) {
		t.Fatalf("AppInstanceIDs = %v; want [app]", got)
	}
}
