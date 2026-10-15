// adr: 1151 — a wake records its transition with the RUNNING publication,
// once, and clears a snapshot-miss backoff only when one is recorded.
package sched

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// runningTransitions counts the instance's transition events of kind that
// end in RUNNING.
func runningTransitions(t *testing.T, store state.Store, instanceID, kind string) int {
	t.Helper()
	events, err := store.ListEvents(t.Context(), instanceID, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range events {
		var data struct {
			To string `json:"to"`
		}
		if ev.Kind == kind && ev.Actor == "schedd" && json.Unmarshal(ev.Data, &data) == nil && data.To == string(state.StateRunning) {
			n++
		}
	}
	return n
}

func TestThawCommitsItsBookkeepingWithThePublication(t *testing.T) {
	for _, tc := range []struct {
		name         string
		missRecorded bool
	}{
		{name: "no snapshot miss recorded"},
		{name: "snapshot miss recorded", missRecorded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFrozenFixture(t, true)
			f.e.EnableFrozenTier()
			if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || !handled {
				t.Fatalf("FreezeIdle: %v, %v", handled, err)
			}
			if tc.missRecorded {
				if err := f.store.DeploymentRecordSnapshotMiss(t.Context(), f.dep.ID, time.Now().Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			counter := countReads(f)
			if result, err := f.e.Wake(t.Context(), f.app.ID, "", "", ""); err != nil || result.InstanceID != f.ins.ID || f.vmm.thaws != 1 {
				t.Fatalf("Wake: %+v, %v, thaws %d", result, err, f.vmm.thaws)
			}
			if n := runningTransitions(t, f.store, f.ins.ID, "frozen_thaw"); n != 1 {
				t.Fatalf("frozen_thaw events = %d, want the one committed with the publication", n)
			}
			if !tc.missRecorded {
				// The clear would run on its own goroutine; give a wrongly
				// started one the time to show up.
				time.Sleep(50 * time.Millisecond)
				if n := counter.reads()["DeploymentClearSnapshotBackoff"]; n != 0 {
					t.Fatalf("cleared an unrecorded backoff %d times", n)
				}
				return
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				_, active, err := f.store.DeploymentSnapshotBackoffActive(t.Context(), f.dep.ID)
				if err == nil && !active && counter.reads()["DeploymentClearSnapshotBackoff"] == 1 {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("recorded backoff not cleared: active %v, err %v, clears %d", active, err, counter.reads()["DeploymentClearSnapshotBackoff"])
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

func TestColdBootRecordsItsTransitionOnce(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	notif := &fakeNotifier{}
	e := newEngine(t, store, &fakeVMM{}, notif, "1.10.0")
	result, err := e.Wake(t.Context(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if n := runningTransitions(t, store, result.InstanceID, "state_transition"); n != 1 {
		t.Fatalf("state_transition events to running = %d, want 1", n)
	}
	// Without WithNotifyInPublication the engine's notifier still sends it.
	notif.mu.Lock()
	defer notif.mu.Unlock()
	sent := 0
	for _, ev := range notif.events {
		if ev.channel == db.NotifyInstanceChanged && ev.payload == instanceChangedPayload(result.InstanceID, app.ID, state.StateRunning, result.WakeID) {
			sent++
		}
	}
	if sent != 1 {
		t.Fatalf("instance_changed for the running instance sent %d times, want 1: %+v", sent, notif.events)
	}
}
