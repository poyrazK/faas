// adr: 20261123190507 — a memory pressure eviction says why in its event.
package sched_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A running instance evicted under RAM pressure records a memory_eviction
// transition with the trigger as its reason; an ordinary Evict (a rollout
// stopping a replica) keeps the plain state_transition kind.
func TestEvictUnderPressureRecordsMemoryEviction(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	since := time.Now().Add(-time.Minute)

	pressured, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if err := e.EvictUnderPressure(ctx, pressured.InstanceID, sched.EvictionReasonMeasuredMemory); err != nil {
		t.Fatalf("EvictUnderPressure: %v", err)
	}
	ins, err := store.InstanceByID(ctx, pressured.InstanceID)
	if err != nil || ins.State != string(state.StateStopped) {
		t.Fatalf("instance = %+v err=%v, want stopped", ins, err)
	}
	rows, err := store.ListEventsByKindSince(ctx, sched.MemoryEvictionEventKind, since, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Subject == nil {
		t.Fatalf("memory_eviction rows = %+v", rows)
	}
	var data struct{ From, To, Reason string }
	if err := json.Unmarshal(rows[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Reason != sched.EvictionReasonMeasuredMemory || data.From != string(state.StateRunning) || data.To != string(state.StateStopped) {
		t.Fatalf("event data = %+v", data)
	}

	stopped, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("second Wake: %v", err)
	}
	if err := e.Evict(ctx, stopped.InstanceID); err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if rows, _ := store.ListEventsByKindSince(ctx, sched.MemoryEvictionEventKind, since, 10); len(rows) != 1 {
		t.Fatalf("a plain Evict recorded a memory eviction: %+v", rows)
	}
}
