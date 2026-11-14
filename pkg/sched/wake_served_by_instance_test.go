// An explicit wake answered by an instance that keeps its own wake_id still
// ends its own timeline (#6639).
package sched_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production 2026-11-05 (rc.324): `gregale app wake --wait` on an app whose
// only instance was frozen timed out. schedd thawed the VM in 30 ms under the
// VM's own wake_id, so the requested wake's timeline stopped at
// app.wake_requested and nothing ran under the id the CLI waited on.
func TestExplicitWakeRecordsTheInstanceThatAnsweredIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		freeze bool
	}{
		{name: "frozen instance named by apid", freeze: true},
		{name: "instance already running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := sched.NewFrozenFixture(t, true)
			e, store, ins := *f.E(), *f.Store(), *f.Ins()
			e.EnableFrozenTier()
			e.WithEvents(events.NewPlatform("schedd", store, sched.TestLog(), nil, nil))
			ctx := t.Context()
			request := map[string]string{"app_id": f.App().ID, "wake_id": uuid.NewString()}
			if tc.freeze {
				if handled, err := e.FreezeIdle(ctx, ins.ID, 150); err != nil || !handled {
					t.Fatalf("FreezeIdle: %v, %v", handled, err)
				}
				request["instance_id"] = ins.ID
			}
			payload, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			loop := sched.NewLoop(nil, e, sched.TestLog())
			if err := loop.HandleDurableNotification(ctx, db.Notification{Channel: db.NotifyAppWake, Payload: string(payload)}); err != nil {
				t.Fatalf("explicit wake: %v", err)
			}
			row, err := store.InstanceByID(ctx, ins.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.State(row.State) != state.StateRunning || row.WakeID != ins.WakeID {
				t.Fatalf("instance after wake = %s under wake %s, want running under its own wake %s", row.State, row.WakeID, ins.WakeID)
			}
			served := servedByInstanceRow(t, store, request["wake_id"])
			if served["instance_id"] != ins.ID || served["instance_wake_id"] != ins.WakeID || served["app_id"] != f.App().ID {
				t.Fatalf("%s payload = %v, want instance %s with wake %s", events.WakeServedByInstance, served, ins.ID, ins.WakeID)
			}
		})
	}
}

// servedByInstanceRow waits for the wake.served_by_instance row of wakeID;
// EmitAsync writes on a worker.
func servedByInstanceRow(t *testing.T, store *state.MemStore, wakeID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, err := store.ListEventsByWakeID(context.Background(), wakeID, time.Time{}, 0)
		if err != nil {
			t.Fatalf("ListEventsByWakeID: %v", err)
		}
		for _, row := range rows {
			if row.Kind != events.WakeServedByInstance {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal(row.Data, &payload); err != nil {
				t.Fatalf("decode %s: %v", row.Kind, err)
			}
			return payload
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s row for wake %s", events.WakeServedByInstance, wakeID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
