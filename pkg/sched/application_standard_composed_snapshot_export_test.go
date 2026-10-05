// adr: 593
package sched

// adr: 435, 581. Test-only exports let the external acceptance test compose the
// actual imaged owner with the scheduler without introducing an import cycle.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type ApplicationStandardSnapshotTestHooks struct {
	Notifier Notifier
	Publish  func(context.Context, db.Notification) error
	Refresh  func(*Engine, state.ApplicationStandardRuntimeRefreshRequest) error
}

type composedSnapshotNotifier struct {
	*fakeNotifier
	next Notifier
}

func (n *composedSnapshotNotifier) Notify(ctx context.Context, channel, payload string) error {
	if n.next != nil {
		if err := n.next.Notify(ctx, channel, payload); err != nil {
			return err
		}
	}
	return n.fakeNotifier.Notify(ctx, channel, payload)
}

func RunApplicationStandardComposedSnapshots(t *testing.T, store state.Store, configure func(string) ApplicationStandardSnapshotTestHooks) {
	t.Helper()
	s, ok := store.(composedWaveStore)
	if !ok {
		t.Fatal("composed snapshot store lacks durable acceptance contracts")
	}
	f := newComposedWaveFixture(t, s)
	hooks := configure(f.vmm.identity.NodeID)
	n := &composedSnapshotNotifier{fakeNotifier: &fakeNotifier{}, next: hooks.Notifier}
	f.engine.notif = n
	if hooks.Refresh != nil {
		f.refresh = func(t *testing.T, r state.ApplicationStandardRuntimeRefreshRequest) {
			t.Helper()
			if err := hooks.Refresh(f.engine, r); err != nil {
				t.Fatal("durable runtime handoff", err)
			}
		}
	}
	f.onboard(t)
	old := map[string]state.Snapshot{}
	oldEvents := map[string]db.Notification{}
	for id := range f.apps {
		ins := f.assertRuntime(t, id, 1)
		event := f.parkComposedSnapshot(t, n, ins)
		oldEvents[id] = event
		old[id] = f.publishComposedSnapshot(t, hooks, id, event, 1)
	}
	if f.engine.Ledger().ResidentRAM() != 0 || f.vmm.captures != 3 || f.vmm.snapshots != 0 {
		t.Fatal("measured park used legacy capture or retained resident RAM")
	}
	f.publish(t, 1, 1)
	op := f.reviewAndApprove(t, 2, 1, 3)
	op = f.materialize(t, op.ID)
	id := op.Targets[0].AppID
	app, dep := f.apps[id], f.deps[id]
	if _, err := s.LatestSnapshot(t.Context(), dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("standard update retained old usable cache", err)
	}
	for other, prior := range old {
		if other == id {
			continue
		}
		current, err := s.LatestSnapshot(t.Context(), prior.DeploymentID)
		if err != nil || current.ID != prior.ID {
			t.Fatal("first wave invalidated a queued service", err)
		}
	}
	r, err := s.GetApplicationStandardRuntimeRefresh(t.Context(), app.OrgID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.refreshRuntime(t, r)
	f.refreshRuntime(t, r)
	if f.vmm.coldBoots != 3 || f.vmm.restores != 0 || f.engine.Ledger().ResidentRAM() != 0 {
		t.Fatal("idle runtime handoff started a VM")
	}
	if err := hooks.Publish(t.Context(), oldEvents[id]); err != nil {
		t.Fatal("late historical publication", err)
	}
	if _, err := s.LatestSnapshot(t.Context(), dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("late publication resurrected stale cache", err)
	}
	f.assertCaptureHistory(t, id, old[id], 1)
	f.wakeComposedSnapshot(t, id, 2, vmmdpb.WakeMethod_WAKE_COLD_BOOT)
	if f.vmm.coldBoots != 4 || f.vmm.restores != 0 {
		t.Fatal("updated service restored obsolete cache")
	}
	poolSize := 1
	if _, err := s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetWarmPoolSize: true, WarmPoolSize: &poolSize}); err != nil {
		t.Fatal(err)
	}
	current := f.assertRuntime(t, id, 2)
	freshEvent := f.parkComposedSnapshot(t, n, current)
	fresh := f.publishComposedSnapshot(t, hooks, id, freshEvent, 2)
	if fresh.ApplicationStandardCaptureToken == old[id].ApplicationStandardCaptureToken || fresh.ID == old[id].ID {
		t.Fatal("cache refresh reused old capture identity")
	}
	// A scheduler restart must recover solely from durable rows and current
	// authority. The native process incarnation remains unchanged here.
	f.engine = newEngine(t, s, f.vmm, n, "1.10.0")
	var recoveryLog bytes.Buffer
	f.engine.log = slog.New(slog.NewTextHandler(&recoveryLog, nil))
	if err := f.engine.ReconcileWarmPool(t.Context(), app.ID); err != nil {
		t.Fatal("paused restore of current cache", err)
	}
	rows, err := s.ListInstancesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var warm state.Instance
	for _, row := range rows {
		if row.State == string(state.StateWarm) {
			if warm.ID != "" {
				t.Fatal("warm pool exceeded its configured size")
			}
			warm = row
		}
	}
	if warm.ID != "" || f.vmm.restores != 0 || f.vmm.coldBoots != 4 || f.engine.Ledger().ResidentRAM() != 0 || !bytes.Contains(recoveryLog.Bytes(), []byte("native capability unavailable")) {
		t.Fatal("unsupported measured paused restore allocated a resident VM", rows, recoveryLog.String())
	}
	// Source-verified native restore still falls back to cold boot. Confirm
	// that the current cache was selected without pretending its bytes ran.
	f.wakeComposedSnapshot(t, id, 2, vmmdpb.WakeMethod_WAKE_COLD_BOOT)
	if f.vmm.lastRequest.GetRestore() == nil || f.vmm.lastRequest.GetRestore().Snapshot.StorageKey != fresh.StorageKey {
		t.Fatal("current cache did not survive scheduler restart")
	}
	for other := range f.apps {
		if other != id {
			f.wakeComposedSnapshot(t, other, 1, vmmdpb.WakeMethod_WAKE_COLD_BOOT)
		}
	}
	f.consume(t)
	op = f.work(t, op.ID, true)
	f.assertWave(t, op, 1, 1)
	if f.vmm.coldBoots != 7 || f.vmm.restores != 0 || f.vmm.captures != 4 || f.vmm.snapshots != 0 {
		t.Fatal("composed snapshot lifecycle bypassed current measured cache")
	}
	t.Log("portable composed snapshot acceptance: 3 inherited services; stale publication refused; idle handoff; current cold capture; restarted cache selection; unavailable paused restore; verified cold fallback; aggregate wave qualification")
}

func (f *composedWaveFixture) wakeComposedSnapshot(t *testing.T, id string, revision int64, method vmmdpb.WakeMethod) {
	t.Helper()
	if _, err := f.engine.Wake(t.Context(), f.apps[id].ID, f.deps[id].ID, "", TriggerGateway); err != nil {
		t.Fatal("composed snapshot wake", id, err)
	}
	ins := f.assertRuntime(t, id, revision)
	if f.vmm.receipts[ins.ID].Method != method {
		t.Fatal("wake chose the wrong cache path", id, f.vmm.receipts[ins.ID].Method, method)
	}
}

func (f *composedWaveFixture) parkComposedSnapshot(t *testing.T, n *composedSnapshotNotifier, ins state.Instance) db.Notification {
	t.Helper()
	n.reset()
	if err := f.engine.Park(t.Context(), ins.ID); err != nil {
		t.Fatal("composed measured park", err)
	}
	actual, err := f.s.InstanceByID(t.Context(), ins.ID)
	if err != nil || actual.State != string(state.StateParked) || f.engine.Ledger().ResidentFor(ins.ID) || n.count(db.NotifySnapshotWritten) != 1 {
		t.Fatal("park did not release RAM and hand publication to imaged", actual, err)
	}
	for _, event := range n.events {
		if event.channel == db.NotifySnapshotWritten {
			var payload map[string]json.RawMessage
			if err := json.Unmarshal([]byte(event.payload), &payload); err != nil || len(payload["application_standard_capture_token"]) == 0 || payload["grant"] != nil || payload["acknowledgment"] != nil {
				t.Fatal("snapshot notification lost its reference-only contract", err)
			}
			return db.Notification{Channel: event.channel, Payload: event.payload}
		}
	}
	t.Fatal("snapshot notification missing")
	return db.Notification{}
}

func (f *composedWaveFixture) publishComposedSnapshot(t *testing.T, hooks ApplicationStandardSnapshotTestHooks, id string, n db.Notification, revision int64) state.Snapshot {
	t.Helper()
	if err := hooks.Publish(t.Context(), n); err != nil {
		t.Fatal("imaged snapshot publication", err)
	}
	snapshot, err := f.s.LatestSnapshotForTier(t.Context(), f.deps[id].ID, state.SnapshotTierInit)
	if err != nil || snapshot.ApplicationStandardCaptureToken == "" {
		t.Fatal("imaged omitted measured cache", snapshot, err)
	}
	if err := hooks.Publish(t.Context(), n); err != nil {
		t.Fatal("duplicate snapshot publication", err)
	}
	duplicate, err := f.s.LatestSnapshotForTier(t.Context(), snapshot.DeploymentID, snapshot.Tier)
	if err != nil || duplicate.ID != snapshot.ID {
		t.Fatal("duplicate publication replaced immutable cache", err)
	}
	f.assertCaptureHistory(t, id, snapshot, revision)
	return snapshot
}

func (f *composedWaveFixture) assertCaptureHistory(t *testing.T, id string, snapshot state.Snapshot, revision int64) state.ApplicationStandardSnapshotCaptureRecord {
	t.Helper()
	app := f.apps[id]
	r, err := f.s.GetApplicationStandardSnapshotCapture(t.Context(), uuid.MustParse(app.AccountID).String(), id, uuid.MustParse(snapshot.DeploymentID).String(), snapshot.ApplicationStandardCaptureToken)
	if err != nil || r.Acknowledgment == nil || r.Grant.Parent.Binding.DesiredRevision != revision || r.Grant.Parent.Binding.Incarnation != f.vmm.identity.Incarnation || r.Grant.MemoryKey != snapshot.StorageKey || r.Grant.Parent.Binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion || !r.Acknowledgment.Grant.Equal(r.Grant) {
		t.Fatal("catalog lost immutable measured capture history", r, err)
	}
	return r
}
