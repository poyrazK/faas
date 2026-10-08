// adr: 712
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAlarmIndexRepairsMissingPublicationAfterCommit(t *testing.T) {
	f := newFixture(t)
	f.manager.store = cleanupFaultStore{memoryStore: f.store, put: func(ctx context.Context, key string, body []byte, version string) (string, error) {
		if strings.HasPrefix(key, alarmIndexPrefix) {
			return "", errors.New("index unavailable")
		}
		return f.store.Put(ctx, key, body, version)
	}}
	alarm := scheduleTestAlarm(t, f) // The committed state still acknowledges.
	manager := openManager(t, f.store, f.clock)
	page, err := manager.ScanIndexedDueAlarms(t.Context(), "")
	if err != nil || len(page.Alarms) != 0 {
		t.Fatal("missing publication unexpectedly indexed", page, err)
	}
	if page, err = manager.ScanDueAlarms(t.Context(), ""); err != nil || len(page.Alarms) != 1 {
		t.Fatal("reconciliation failed", page, err)
	}
	page, err = manager.ScanIndexedDueAlarms(t.Context(), "")
	if err != nil || len(page.Alarms) != 1 || page.Alarms[0].Entity != alarm.Entity || page.Alarms[0].Version != alarm.Version || !page.Alarms[0].At.Equal(alarm.At) {
		t.Fatal("missing hint not repaired", page, err)
	}
	// A replacement/clear leaves a stale immutable hint which is safe to prune.
	if _, err := manager.InvokeAlarm(t.Context(), alarm, "worker", consumeTestAlarm); err != nil {
		t.Fatal(err)
	}
	page, err = manager.ScanIndexedDueAlarms(t.Context(), "")
	if err != nil || page.Failed != 0 || len(page.Alarms) != 0 {
		t.Fatal("cleared alarm remained deliverable", page, err)
	}
	objects, err := f.store.ListEntityObjects(t.Context(), alarmIndexPrefix, "", api.DurableEntityCleanupPageSize)
	if err != nil || len(objects.Keys) != 1 {
		// The reservation's future recovery hint remains until its due time.
		t.Fatal(objects, err)
	}
	f.clock.Add(int64(api.DurableEntityAlarmRetryBase))
	if _, err := manager.ScanIndexedDueAlarms(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	objects, err = f.store.ListEntityObjects(t.Context(), alarmIndexPrefix, "", api.DurableEntityCleanupPageSize)
	if err != nil || len(objects.Keys) != 0 {
		t.Fatal("stale recovery hint not collected", objects, err)
	}
}

type alarmIndexReadStore struct {
	*memoryStore
	manifestReads int
}

func (s *alarmIndexReadStore) Get(ctx context.Context, key string, maxBytes int64) ([]byte, string, error) {
	if strings.HasSuffix(key, "/manifest.json") {
		s.manifestReads++
	}
	return s.memoryStore.Get(ctx, key, maxBytes)
}

func TestAlarmIndexPaginatesDueWorkWithoutReadingDormantOrFutureEntities(t *testing.T) {
	f := newFixture(t)
	now := time.Unix(0, f.clock.Load())
	for i := range 40 {
		id := f.id
		id.Key = fmt.Sprintf("indexed-%d", i)
		var at *time.Time
		switch {
		case i < 11:
			at = &now
		case i < 20:
			future := now.Add(time.Hour)
			at = &future
		}
		if _, err := f.manager.Invoke(t.Context(), id, "caller", request("schedule"), func(context.Context, View) (Transition, error) {
			return Transition{Data: json.RawMessage(`{}`), Result: json.RawMessage(`null`), AlarmAt: at}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	store := &alarmIndexReadStore{memoryStore: f.store}
	manager := openManager(t, store, f.clock)
	cursor, due, pages := "", 0, 0
	for {
		page, err := manager.ScanIndexedDueAlarms(t.Context(), cursor)
		if err != nil || page.Failed != 0 || len(page.Alarms) > api.DurableEntityAlarmScanPageSize {
			t.Fatal(page, err)
		}
		due += len(page.Alarms)
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if due != 11 || pages != 2 || store.manifestReads != 11 {
		t.Fatalf("due=%d pages=%d manifest reads=%d", due, pages, store.manifestReads)
	}
}

func TestAlarmIndexRepairsCrashDuringReservedAttemptAndPrunesOldRetryHints(t *testing.T) {
	f := newFixture(t)
	alarm := scheduleTestAlarm(t, f)
	claim, err := f.manager.Acquire(t.Context(), f.id, "crashed-worker")
	if err != nil {
		t.Fatal(err)
	}
	f.manager.store = cleanupFaultStore{memoryStore: f.store, put: func(ctx context.Context, key string, body []byte, version string) (string, error) {
		if strings.HasPrefix(key, alarmIndexPrefix) {
			return "", errors.New("crash before index publication")
		}
		return f.store.Put(ctx, key, body, version)
	}}
	if err := f.manager.reserveAlarmAttempt(t.Context(), claim, alarm); err != nil {
		t.Fatal(err)
	}
	restarted := openManager(t, f.store, f.clock)
	if page, err := restarted.ScanDueAlarms(t.Context(), ""); err != nil || len(page.Alarms) != 0 {
		t.Fatal("reconciliation ignored backoff", page, err)
	}
	if page, err := restarted.ScanIndexedDueAlarms(t.Context(), ""); err != nil || len(page.Alarms) != 0 {
		t.Fatal("old index bypassed retry reservation", page, err)
	}
	f.clock.Store(claim.ExpiresAt.Add(time.Second).UnixNano())
	page, err := restarted.ScanIndexedDueAlarms(t.Context(), "")
	if err != nil || len(page.Alarms) != 1 || page.Alarms[0].Version != 1 {
		t.Fatal("crashed reservation was not recoverable", page, err)
	}
	if _, err := restarted.InvokeAlarm(t.Context(), page.Alarms[0], "replacement", consumeTestAlarm); err != nil {
		t.Fatal(err)
	}
	assertCount(t, t.Context(), restarted, f.id, 2, 2)
}

func TestAlarmIndexRejectsUnsafePagesAndRepairsCorruptHints(t *testing.T) {
	f := newFixture(t)
	alarm := scheduleTestAlarm(t, f)
	entry := alarmIndexEntry{Alarm: alarm, DueAt: alarm.At}
	_, version, err := f.store.Get(t.Context(), entry.key(), api.MaxDurableEntityAlarmIndexBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Put(t.Context(), entry.key(), []byte(`{}`), version); err != nil {
		t.Fatal(err)
	}
	page, err := f.manager.ScanIndexedDueAlarms(t.Context(), "")
	if err != nil || page.Failed != 1 || len(page.Alarms) != 0 {
		t.Fatal("corrupt hint became authority", page, err)
	}
	if _, err := f.manager.ScanDueAlarms(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	page, err = f.manager.ScanIndexedDueAlarms(t.Context(), "")
	if err != nil || page.Failed != 0 || len(page.Alarms) != 1 {
		t.Fatal("corrupt hint not repaired", page, err)
	}
	for _, bad := range []CleanupObjects{
		{Keys: []string{f.id.prefix() + "manifest.json"}},
		{Keys: make([]string, api.DurableEntityAlarmScanPageSize+1)},
		{NextCursor: "without-entries"},
		{Keys: []string{entry.key(), entry.key()}},
	} {
		store := cleanupFaultStore{memoryStore: f.store, list: func(context.Context, string, string, int32) (CleanupObjects, error) { return bad, nil }, remove: func(context.Context, string) error {
			t.Fatal("unsafe listing triggered deletion")
			return nil
		}}
		manager := openManager(t, store, f.clock)
		if _, err := manager.ScanIndexedDueAlarms(t.Context(), ""); !errors.Is(err, ErrCorrupt) {
			t.Fatal("invalid index page accepted", bad, err)
		}
	}
}
