// adr: 471 — migration preserves IDs; delayed failures cannot stop the new node.
package sched_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFailureReportSourceRefusesMovedInstance(t *testing.T) {
	for _, kind := range []string{sched.InstanceFailureLiveness, sched.InstanceFailureWorkloadOOM} {
		t.Run(kind, func(t *testing.T) {
			store := state.NewMemStore()
			_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
			vmm := &sched.FakeVMM{}
			e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.7.0")
			ins := sched.RunningInstance(t, store, app, dep, vmm, e)
			// The durable row/ledger identify the destination; the report was
			// observed on a previous node with this same logical instance ID.
			ctx := sched.WithFailureReportSourceNode(t.Context(), "previous-node")
			var err error
			if kind == sched.InstanceFailureLiveness {
				err = e.DestroyForLivenessFailure(ctx, ins.ID, "timeout")
			} else {
				err = e.DestroyForWorkloadOOMFailure(ctx, ins.ID, 300, 256)
			}
			if !errors.Is(err, sched.ErrFailureReportSourceChanged) {
				t.Fatalf("direct stale source: %v", err)
			}
			payload := `{"instance_id":"` + ins.ID + `","app_id":"` + app.ID + `","source_node_id":"previous-node","kind":"` + kind + `"}`
			if err := e.HandleRelayedInstanceFailure(t.Context(), payload); !errors.Is(err, sched.ErrFailureReportSourceChanged) {
				t.Fatalf("relayed stale source: %v", err)
			}
			fresh, err := store.InstanceByID(t.Context(), ins.ID)
			if err != nil || fresh.State != string(state.StateRunning) || !e.Ledger().ResidentFor(ins.ID) {
				t.Fatalf("destination state/admission changed: %+v, %v", fresh, err)
			}
			(*vmm.Mu()).Lock()
			defer (*vmm.Mu()).Unlock()
			if (*vmm.Destroys()) != 0 {
				t.Fatal("stale report destroyed destination guest")
			}
		})
	}
}
