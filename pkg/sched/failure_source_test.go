// adr: 471 — migration preserves IDs; delayed failures cannot stop the new node.
package sched

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFailureReportSourceRefusesMovedInstance(t *testing.T) {
	for _, kind := range []string{InstanceFailureLiveness, InstanceFailureWorkloadOOM} {
		t.Run(kind, func(t *testing.T) {
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
			vmm := &fakeVMM{}
			e := newEngine(t, store, vmm, &fakeNotifier{}, "1.7.0")
			ins := runningInstance(t, store, app, dep, vmm, e)
			// The durable row/ledger identify the destination; the report was
			// observed on a previous node with this same logical instance ID.
			ctx := WithFailureReportSourceNode(t.Context(), "previous-node")
			var err error
			if kind == InstanceFailureLiveness {
				err = e.DestroyForLivenessFailure(ctx, ins.ID, "timeout")
			} else {
				err = e.DestroyForWorkloadOOMFailure(ctx, ins.ID, 300, 256)
			}
			if !errors.Is(err, ErrFailureReportSourceChanged) {
				t.Fatalf("direct stale source: %v", err)
			}
			payload := `{"instance_id":"` + ins.ID + `","app_id":"` + app.ID + `","source_node_id":"previous-node","kind":"` + kind + `"}`
			if err := e.HandleRelayedInstanceFailure(t.Context(), payload); !errors.Is(err, ErrFailureReportSourceChanged) {
				t.Fatalf("relayed stale source: %v", err)
			}
			fresh, err := store.InstanceByID(t.Context(), ins.ID)
			if err != nil || fresh.State != string(state.StateRunning) || !e.Ledger().ResidentFor(ins.ID) {
				t.Fatalf("destination state/admission changed: %+v, %v", fresh, err)
			}
			vmm.mu.Lock()
			defer vmm.mu.Unlock()
			if vmm.destroys != 0 {
				t.Fatal("stale report destroyed destination guest")
			}
		})
	}
}
