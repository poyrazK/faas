// adr: 20261106214916

package sched_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// setVCPUApps sets guest_vcpu_from_cpu_apps as schedd does: a revision, then
// the engine applies it.
func (f *refreshFixture) setVCPUApps(t *testing.T, list string) {
	t.Helper()
	value, _ := json.Marshal(list)
	if _, err := f.store.UpsertRuntimeConfig(f.ctx, state.RuntimeConfigUpdate{Key: runtimeconfig.KeyGuestVCPUFromCPUApps, DesiredValue: value}); err != nil {
		t.Fatalf("UpsertRuntimeConfig: %v", err)
	}
	if err := f.engine.SetGuestVCPUFromCPUApps([]string{list}); err != nil {
		t.Fatal(err)
	}
}

// A snapshot from an instance that started before the vCPU count its app
// boots with changed would be refused at restore: the pass counts it stale
// without asking vmmd. One from an instance started after the change is
// asked about as usual.
func TestSnapshotRefreshPassFindsSnapshotsAtAnotherVCPUCount(t *testing.T) {
	f := newRefreshFixture(t)
	f.shadow(t)
	_, before := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{CreatedAt: time.Now().UTC().Add(-2 * time.Hour)})
	f.vmm.verdicts[before] = sched.SnapshotBackingCurrent
	// Pro apps go from 2 vCPUs to 1.
	f.setVCPUApps(t, "*")
	_, after := f.app(t, f.nodeA.ID, 2*time.Hour, state.Snapshot{CreatedAt: time.Now().UTC()})
	f.vmm.verdicts[after] = sched.SnapshotBackingCurrent

	pass, err := f.refresher.Pass(f.ctx, f.nodeA.ID, "r1")
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if pass.Outcomes[sched.SnapshotRefreshWouldRefresh] != 1 || pass.Outcomes[sched.SnapshotRefreshCurrent] != 1 {
		t.Fatalf("pass outcomes = %v, want one would_refresh and one current", pass.Outcomes)
	}
	if len(f.vmm.asked) != 1 || f.vmm.asked[0] != f.nodeA.ID+"|"+after {
		t.Fatalf("vmmd asked %v, want only the snapshot captured after the change", f.vmm.asked)
	}

	// Back to the plan's count: the old snapshot is current again, and the
	// one captured at 1 vCPU is stale.
	time.Sleep(2 * time.Millisecond)
	f.setVCPUApps(t, "")
	f.vmm.asked = nil
	pass, err = f.refresher.Pass(f.ctx, f.nodeA.ID, "r1")
	if err != nil || pass.Outcomes[sched.SnapshotRefreshCurrent] != 1 || pass.Outcomes[sched.SnapshotRefreshWouldRefresh] != 1 {
		t.Fatalf("after the list emptied: pass = %+v (err %v), want the old snapshot current and the 1-vCPU one stale", pass, err)
	}
}

// Settings the history cannot explain (a list applied with no revision)
// leave the vCPU count to the restore: the pass asks vmmd as before.
func TestSnapshotRefreshPassWithoutVCPUHistoryAsksVMMD(t *testing.T) {
	f := newRefreshFixture(t)
	f.shadow(t)
	_, key := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{CreatedAt: time.Now().UTC().Add(-2 * time.Hour)})
	f.vmm.verdicts[key] = sched.SnapshotBackingCurrent
	if err := f.engine.SetGuestVCPUFromCPUApps([]string{"*"}); err != nil {
		t.Fatal(err)
	}
	pass, err := f.refresher.Pass(f.ctx, f.nodeA.ID, "r1")
	if err != nil || pass.Outcomes[sched.SnapshotRefreshCurrent] != 1 || len(f.vmm.asked) != 1 {
		t.Fatalf("pass = %+v (err %v), asked %v; want vmmd asked and the snapshot current", pass, err, f.vmm.asked)
	}
}

// A change to the vCPU settings runs every node's pass again, with no new
// release.
func TestSnapshotRefreshTickRunsAgainWhenTheVCPUSettingsChange(t *testing.T) {
	f := newRefreshFixture(t)
	f.shadow(t)
	_, key := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{CreatedAt: time.Now().UTC().Add(time.Hour)})
	f.vmm.verdicts[key] = sched.SnapshotBackingCurrent

	f.refresher.Tick(f.ctx)
	f.refresher.Tick(f.ctx)
	if len(f.vmm.asked) != 1 {
		t.Fatalf("vmmd asked %v, want one pass", f.vmm.asked)
	}
	// Another app is listed: this one keeps its count, so vmmd is asked.
	f.setVCPUApps(t, uuid.NewString())
	f.refresher.Tick(f.ctx)
	f.refresher.Tick(f.ctx)
	if len(f.vmm.asked) != 2 {
		t.Fatalf("vmmd asked %v, want a second pass after the change", f.vmm.asked)
	}
}
