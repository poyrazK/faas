// adr: 123 — wake.boot_started names the invocation drain, not an internal daemon.
package sched

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDrainWakeIsAttributedToInvocation(t *testing.T) {
	d, store, _, _, _ := newDrainHarness(t, api.PlanHobby, true)
	d.engine.WithEvents(events.NewPlatform("schedd", store, testLog(), nil, nil))
	inv := seedDrainInvocation(t, store, state.InvocationQueue)

	d.Tick(context.Background())

	if got, err := store.InvocationByID(context.Background(), inv.ID); err != nil || got.State != state.InvocationCompleted {
		t.Fatalf("invocation = %+v, %v; want completed", got.State, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := store.ListEvents(context.Background(), "", 0)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		for _, row := range rows {
			if row.Kind != events.WakeBootStarted {
				continue
			}
			var data map[string]any
			if err := json.Unmarshal(row.Data, &data); err != nil {
				t.Fatalf("decode boot_started: %v", err)
			}
			if data["trigger"] != TriggerInvocation {
				t.Fatalf("drain wake trigger = %v, want %q", data["trigger"], TriggerInvocation)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("drain wake emitted no wake.boot_started event")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
