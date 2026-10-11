package objectstorage_test

// adr: 712

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func TestGCSEntityInventoryCapCleanupAndAlarmWireConformance(t *testing.T) {
	wire, provider := newGCSEntityWire(t)
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC).UnixNano())
	m := gcsEntityManager(t, provider, clock)
	id := durableentity.ID{AccountID: "account", AppID: "app", Namespace: "counters", Key: "operations"}
	claim, err := m.Acquire(t.Context(), id, "operator")
	if err != nil {
		t.Fatal(err)
	}
	for _, requestID := range []string{"first", "second"} {
		if _, err := m.Execute(t.Context(), claim, gcsEntityRequest(requestID), gcsEntityIncrement); err != nil {
			t.Fatal(err)
		}
	}
	qualifyGCSEntityCap(t, m, claim)
	qualifyGCSEntityCleanup(t, m, claim)
	if err := m.Release(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	qualifyGCSEntityAlarm(t, m, id, clock)
	wire.mu.Lock()
	defer wire.mu.Unlock()
	if wire.lists < 2 || wire.deletes == 0 {
		t.Fatal("operations did not exercise native listing and deletion", wire.lists, wire.deletes)
	}
}

func qualifyGCSEntityCap(t *testing.T, m *durableentity.Manager, claim durableentity.Claim) {
	t.Helper()
	var inventory durableentity.InventoryResult
	for range 100 {
		var err error
		inventory, err = m.Inventory(t.Context(), claim)
		if err != nil {
			t.Fatal(err)
		}
		if inventory.Complete {
			break
		}
	}
	if !inventory.Complete || !inventory.CurrentBytesKnown || inventory.Usage.ReceiptCount != 2 || inventory.CurrentBytes <= inventory.Usage.TotalBytes() {
		t.Fatal("GCS inventory did not preserve committed and current-key bytes", inventory)
	}
	if err := m.SetStorageLimit(t.Context(), claim, inventory.Usage.TotalBytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Execute(t.Context(), claim, gcsEntityRequest("over-cap"), gcsEntityIncrement); !errors.Is(err, durableentity.ErrLimit) {
		t.Fatal("GCS committed storage cap was not enforced", err)
	}
	if replay, err := m.Execute(t.Context(), claim, gcsEntityRequest("first"), gcsEntityIncrement); err != nil || !replay.Replayed || replay.Version != 1 {
		t.Fatal("GCS original receipt did not replay at capacity", replay, err)
	}
	if err := m.SetStorageLimit(t.Context(), claim, 0); err != nil {
		t.Fatal(err)
	}
}

func qualifyGCSEntityCleanup(t *testing.T, m *durableentity.Manager, claim durableentity.Claim) {
	t.Helper()
	cursor, deleted, complete := "", 0, false
	for range 100 {
		page, err := m.Collect(t.Context(), claim, cursor)
		if err != nil || page.Failed != 0 {
			t.Fatal("GCS cleanup failed", page, err)
		}
		deleted += page.Deleted
		cursor = page.NextCursor
		if cursor == "" {
			complete = true
			break
		}
	}
	if !complete || deleted == 0 {
		t.Fatal("GCS cleanup did not finish reclaiming unused objects", deleted)
	}
	replay, err := m.Execute(t.Context(), claim, gcsEntityRequest("first"), gcsEntityIncrement)
	if err != nil || !replay.Replayed || replay.Version != 1 || string(replay.Value) != `{"count":1}` {
		t.Fatal("GCS cleanup lost the original receipt", replay, err)
	}
	view, err := m.Read(t.Context(), claim.ID)
	if err != nil || view.Version != 2 || string(view.Data) != `{"count":2}` {
		t.Fatal("GCS cleanup lost current state", view, err)
	}
}

func qualifyGCSEntityAlarm(t *testing.T, m *durableentity.Manager, id durableentity.ID, clock *atomic.Int64) {
	t.Helper()
	at := time.Unix(0, clock.Load()).Add(-time.Second)
	if err := m.CheckAlarmDiscovery(t.Context()); err != nil {
		t.Fatal("GCS alarm index capabilities", err)
	}
	if _, err := m.Invoke(t.Context(), id, "caller", gcsEntityRequest("schedule"), func(ctx context.Context, view durableentity.View) (durableentity.Transition, error) {
		transition, err := gcsEntityIncrement(ctx, view)
		transition.AlarmAt = &at
		return transition, err
	}); err != nil {
		t.Fatal(err)
	}
	page, err := m.ScanIndexedDueAlarms(t.Context(), "")
	if err != nil || page.Failed != 0 || len(page.Alarms) != 1 || page.Alarms[0].Entity != id {
		t.Fatal("GCS time-ordered index lost the due alarm", page, err)
	}
	if _, err := m.InvokeAlarm(t.Context(), page.Alarms[0], "failed-worker", func(context.Context, durableentity.View) (durableentity.Transition, error) {
		return durableentity.Transition{}, errors.New("guest unavailable")
	}); err == nil {
		t.Fatal("GCS failed alarm acknowledged")
	}
	status, err := m.InspectAlarm(t.Context(), id)
	if err != nil || status.Attempts != 1 || status.NextAttemptAt == nil || status.Exhausted {
		t.Fatal("GCS retry reservation not persisted", status, err)
	}
	if _, err := m.InvokeAlarm(t.Context(), page.Alarms[0], "early-worker", gcsEntityIncrement); !errors.Is(err, durableentity.ErrAlarmBackoff) {
		t.Fatal("GCS retry backoff ignored", err)
	}
	clock.Add(int64(api.DurableEntityAlarmRetryBase))
	result, err := m.InvokeAlarm(t.Context(), page.Alarms[0], "alarm-worker", gcsEntityIncrement)
	if err != nil || result.Version != 4 || string(result.Value) != `{"count":4}` {
		t.Fatal("GCS alarm did not commit", result, err)
	}
	replay, err := m.InvokeAlarm(t.Context(), page.Alarms[0], "replacement-worker", gcsEntityIncrement)
	if err != nil || !replay.Replayed || replay.Version != result.Version || string(replay.Value) != string(result.Value) {
		t.Fatal("GCS alarm did not replay after delivery", replay, err)
	}
	page, err = m.ScanDueAlarms(t.Context(), "")
	if err != nil || page.Failed != 0 || len(page.Alarms) != 0 {
		t.Fatal("GCS alarm stayed due after being cleared", page, err)
	}
}
