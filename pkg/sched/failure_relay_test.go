// spec: §6.1
// adr: 078
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Issue #3359: the schedd hosting an instance relays vmmd's failure report
// over pg_notify; only the schedd that owns the app applies it.
func TestRelayedInstanceFailure_OnlyOwnerApplies(t *testing.T) {
	const ownerNode = "node-owner"
	cases := []struct {
		name        string
		handlerNode string
		wantState   state.State
	}{
		{name: "owner destroys", handlerNode: ownerNode, wantState: state.StateStopped},
		{name: "non-owner discards", handlerNode: "node-peer", wantState: state.StateRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
			if err := store.SetAppNodeID(ctx, app.ID, ownerNode); err != nil {
				t.Fatalf("SetAppNodeID: %v", err)
			}
			vmm := &fakeVMM{}

			hostNotif := &fakeNotifier{}
			host := newEngine(t, store, vmm, hostNotif, "1.10.0").WithOwnerNodeID("node-host")
			handler := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOwnerNodeID(tc.handlerNode)
			inst := runningInstance(t, store, app, dep, vmm, handler)

			if err := host.RelayInstanceFailure(ctx, InstanceFailureReport{
				InstanceID: inst.ID, AppID: app.ID, Kind: InstanceFailureLiveness, Reason: "liveness_process_exited",
			}); err != nil {
				t.Fatalf("RelayInstanceFailure: %v", err)
			}
			if got := hostNotif.count(db.NotifyInstanceFailureRelayed); got != 1 {
				t.Fatalf("relay notifications = %d, want 1", got)
			}
			hostNotif.mu.Lock()
			payload := hostNotif.events[len(hostNotif.events)-1].payload
			hostNotif.mu.Unlock()

			if err := handler.HandleRelayedInstanceFailure(ctx, payload); err != nil {
				t.Fatalf("HandleRelayedInstanceFailure: %v", err)
			}
			got, err := store.InstanceByID(ctx, inst.ID)
			if err != nil {
				t.Fatalf("InstanceByID: %v", err)
			}
			if state.State(got.State) != tc.wantState {
				t.Fatalf("instance state = %q, want %q", got.State, tc.wantState)
			}
		})
	}
}

func TestRelayedInstanceFailure_RejectsMalformedReports(t *testing.T) {
	store := state.NewMemStore()
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	ctx := context.Background()
	bad := []InstanceFailureReport{
		{AppID: "a", Kind: InstanceFailureLiveness},
		{InstanceID: "i", Kind: InstanceFailureLiveness},
		{InstanceID: "i", AppID: "a", Kind: "reboot"},
	}
	for _, r := range bad {
		if err := e.RelayInstanceFailure(ctx, r); err == nil {
			t.Errorf("RelayInstanceFailure(%+v) = nil, want validation error", r)
		}
	}
	if err := e.HandleRelayedInstanceFailure(ctx, "{not json"); err == nil {
		t.Error("HandleRelayedInstanceFailure(garbage) = nil, want decode error")
	}
	// An instance that no longer exists is a benign no-op.
	if err := e.HandleRelayedInstanceFailure(ctx, `{"instance_id":"gone","app_id":"a","kind":"liveness"}`); err != nil {
		t.Errorf("HandleRelayedInstanceFailure(missing instance) = %v, want nil", err)
	}
}

// The relay reaches the owner through the scheduler loop's LISTEN arm, and
// both report kinds must land on their destroy path.
func TestLoopAppliesRelayedInstanceFailureKinds(t *testing.T) {
	cases := []struct {
		name    string
		payload func(instanceID, appID string) string
	}{
		{name: "liveness", payload: func(i, a string) string {
			return `{"instance_id":"` + i + `","app_id":"` + a + `","kind":"liveness","reason":"liveness_process_exited"}`
		}},
		{name: "workload_oom", payload: func(i, a string) string {
			return `{"instance_id":"` + i + `","app_id":"` + a + `","kind":"workload_oom","peak_mb":300,"plan_mb":256}`
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
			if err := store.SetAppNodeID(ctx, app.ID, "node-owner"); err != nil {
				t.Fatalf("SetAppNodeID: %v", err)
			}
			vmm := &fakeVMM{}
			owner := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOwnerNodeID("node-owner")
			inst := runningInstance(t, store, app, dep, vmm, owner)
			loop := NewLoop(nil, owner, testLog())

			loop.handleNotification(ctx, db.Notification{
				Channel: db.NotifyInstanceFailureRelayed,
				Payload: tc.payload(inst.ID, app.ID),
			})

			got, err := store.InstanceByID(ctx, inst.ID)
			if err != nil {
				t.Fatalf("InstanceByID: %v", err)
			}
			if state.State(got.State) != state.StateStopped {
				t.Fatalf("instance state = %q, want %q", got.State, state.StateStopped)
			}
		})
	}
}
