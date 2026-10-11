// adr:684
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type imageRecoverySnapshotStore struct {
	state.Store
	fail bool
}

func (s *imageRecoverySnapshotStore) MarkSnapshotStale(ctx context.Context, id string) error {
	if s.fail {
		return errors.New("snapshot storage unavailable")
	}
	return s.Store.MarkSnapshotStale(ctx, id)
}

func TestImageHealthcheckRecoveryStopsServingAndForcesColdBoot(t *testing.T) {
	for _, failInvalidation := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovery", true: "retry invalidation"}[failInvalidation], func(t *testing.T) {
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
			wrapper := &imageRecoverySnapshotStore{Store: store, fail: failInvalidation}
			vmm := &fakeVMM{}
			window := NewLivenessWindow(5*time.Minute, 3)
			engine := newEngine(t, wrapper, vmm, &fakeNotifier{}, "1.10.0").WithLivenessWindow(window)
			for _, tier := range []string{state.SnapshotTierInit, state.SnapshotTierWarm} {
				_, err := store.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", Tier: tier, MemBytes: 512 << 20, DiskBytes: 1 << 20, StorageKey: state.SnapshotCaptureMemKey(dep.ID, tier, "image-recovery")})
				if err != nil {
					t.Fatal(err)
				}
			}
			inst := runningInstance(t, store, app, dep, vmm, engine)
			if failInvalidation {
				if err := engine.DestroyForLivenessFailure(t.Context(), inst.ID, fcvm.LivenessReasonImageHealthcheck); err == nil {
					t.Fatal("unsafe recovery ignored invalidation failure")
				}
				row, _ := store.InstanceByID(t.Context(), inst.ID)
				if row.State != string(state.StateRunning) || vmm.destroys != 0 || window.recentOnNode(dep.ID, inst.NodeID, time.Now()) != 0 {
					t.Fatal("invalidation failure lost resident ownership or restart budget")
				}
				wrapper.fail = false
			}
			if err := engine.DestroyForLivenessFailure(t.Context(), inst.ID, fcvm.LivenessReasonImageHealthcheck); err != nil {
				t.Fatal(err)
			}
			row, err := store.InstanceByID(t.Context(), inst.ID)
			if err != nil || row.State != string(state.StateStopped) {
				t.Fatal("failed image remained serving")
			}
			if _, ok, _ := engine.usableSnapshotForWake(t.Context(), dep.ID, string(api.PlanPro), 512, api.AppProtocolHTTP1); ok {
				t.Fatal("recovery can restore an unhealthy snapshot")
			}
			if window.recentOnNode(dep.ID, inst.NodeID, time.Now()) != 1 {
				t.Fatal("confirmed image failure did not consume restart budget")
			}
			if err := engine.DestroyForLivenessFailure(t.Context(), inst.ID, fcvm.LivenessReasonImageHealthcheck); err != nil {
				t.Fatal(err)
			}
			if vmm.destroys != 1 || window.recentOnNode(dep.ID, inst.NodeID, time.Now()) != 1 {
				t.Fatal("redelivered failure double-counted recovery")
			}
		})
	}
}
