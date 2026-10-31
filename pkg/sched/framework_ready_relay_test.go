// spec: §6.1
package sched_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 14 H14-104: the schedd hosting an instance relays vmmd's
// framework-ready receipt over pg_notify; only the app's owner stamps it.
func TestRelayedFrameworkReady_OnlyOwnerStamps(t *testing.T) {
	const ownerNode = "node-owner"
	for _, tc := range []struct {
		name        string
		handlerNode string
		wantStamp   bool
	}{
		{name: "owner stamps", handlerNode: ownerNode, wantStamp: true},
		{name: "non-owner discards", handlerNode: "node-peer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
			if err := store.SetAppNodeID(ctx, app.ID, ownerNode); err != nil {
				t.Fatalf("SetAppNodeID: %v", err)
			}
			vmm := &sched.FakeVMM{}
			hostNotif := &sched.FakeNotifier{}
			host := sched.XNewEngine(t, store, vmm, hostNotif, "1.10.0").WithOwnerNodeID("node-host")
			handler := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOwnerNodeID(tc.handlerNode)
			inst := sched.RunningInstance(t, store, app, dep, vmm, handler)

			if err := host.RelayFrameworkReady(ctx, inst.ID, app.ID); err != nil {
				t.Fatalf("RelayFrameworkReady: %v", err)
			}
			if got := hostNotif.Count(db.NotifyFrameworkReadyRelayed); got != 1 {
				t.Fatalf("relay notifications = %d, want 1", got)
			}
			(*hostNotif.Mu()).Lock()
			payload := (*(*hostNotif.Events())[len((*hostNotif.Events()))-1].Payload())
			(*hostNotif.Mu()).Unlock()

			if err := handler.HandleRelayedFrameworkReady(ctx, payload); err != nil {
				t.Fatalf("HandleRelayedFrameworkReady: %v", err)
			}
			got, err := store.InstanceByID(ctx, inst.ID)
			if err != nil {
				t.Fatalf("InstanceByID: %v", err)
			}
			if (got.FrameworkReadyAt != nil) != tc.wantStamp {
				t.Fatalf("framework_ready_at set = %v, want %v", got.FrameworkReadyAt != nil, tc.wantStamp)
			}
		})
	}
}

func TestRelayedFrameworkReady_RejectsMalformedReports(t *testing.T) {
	e := sched.XNewEngine(t, state.NewMemStore(), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	ctx := context.Background()
	if err := e.RelayFrameworkReady(ctx, "", "app"); err == nil {
		t.Error("relay without instance_id = nil, want an error")
	}
	if err := e.HandleRelayedFrameworkReady(ctx, "{"); err == nil {
		t.Error("undecodable payload = nil, want an error")
	}
	if err := e.HandleRelayedFrameworkReady(ctx, `{"instance_id":"missing","app_id":"a"}`); err != nil {
		t.Errorf("a gone instance needs no stamp: %v", err)
	}
}
