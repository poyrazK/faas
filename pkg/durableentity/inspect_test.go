// adr: 845
package durableentity

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestInspectionObservesHeldEntityWithoutWrites(t *testing.T) {
	f, message := committedOutboxFixture(t, 2)
	at := time.Unix(0, f.clock.Load()).Add(-time.Second)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("alarm"), func(ctx context.Context, view View) (Transition, error) {
		transition, err := increment(ctx, view)
		transition.AlarmAt = &at
		return transition, err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.ReserveOutbox(t.Context(), f.claim, message.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.reserveAlarmAttempt(t.Context(), f.claim, Alarm{Entity: f.id, Version: 2, At: at}); err != nil {
		t.Fatal(err)
	}
	reader := openManager(t, f.store, f.clock)
	before := map[string]memoryObject{}
	f.store.mu.Lock()
	for key, value := range f.store.objects {
		before[key] = value
	}
	f.store.mu.Unlock()
	reader.store = wrappedStore{ObjectStore: f.store, put: func(context.Context, string, []byte, string) (string, error) {
		t.Fatal("inspection wrote storage")
		return "", ErrConflict
	}}
	for range 2 {
		out, err := reader.Inspect(t.Context(), f.id)
		if err != nil || out.Version != 2 || out.Outbox.Pending != 2 || out.Outbox.HeadID != message.ID || out.Outbox.Attempts != 1 || out.Outbox.NextAttemptAt == nil || out.Alarm.Alarm == nil || !out.Alarm.Alarm.At.Equal(at) || out.Alarm.Attempts != 1 || out.Alarm.NextAttemptAt == nil {
			t.Fatalf("inspection = %+v, %v", out, err)
		}
	}
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	if !reflect.DeepEqual(before, f.store.objects) {
		t.Fatal("inspection changed committed objects")
	}
}

func TestInspectionMissingAndBrokenCommittedState(t *testing.T) {
	for _, kind := range []string{"missing", "initial", "corrupt", "missing-snapshot"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			id := f.id
			if kind == "missing" {
				id.Key = "absent"
			}
			if kind == "corrupt" || kind == "missing-snapshot" {
				if _, err := f.manager.Execute(t.Context(), f.claim, request("one"), increment); err != nil {
					t.Fatal(err)
				}
				manifest, _, err := f.manager.readManifest(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				f.store.mu.Lock()
				if kind == "corrupt" {
					object := f.store.objects[manifest.SnapshotKey]
					object.body = []byte(`{`)
					f.store.objects[manifest.SnapshotKey] = object
				} else {
					delete(f.store.objects, manifest.SnapshotKey)
				}
				f.store.mu.Unlock()
			}
			out, err := f.manager.Inspect(t.Context(), id)
			switch kind {
			case "initial":
				if err != nil || out.Version != 0 || out.Outbox.Pending != 0 || out.Alarm.Alarm != nil {
					t.Fatal(out, err)
				}
			case "missing":
				if !errors.Is(err, ErrNotFound) {
					t.Fatal(out, err)
				}
			default:
				if !errors.Is(err, ErrCorrupt) {
					t.Fatal(out, err)
				}
			}
		})
	}
}
