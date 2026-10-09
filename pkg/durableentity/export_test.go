// adr: 850
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestStateRestoreUncertainPublicationReplays(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("seed"), withOutbox()); err != nil {
		t.Fatal(err)
	}
	exported, err := f.manager.ExportState(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	lost := false
	m := openManager(t, wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		version, err := f.store.Put(ctx, key, body, etag)
		if err == nil && strings.HasSuffix(key, "/manifest.json") && !lost {
			lost = true
			return "", errors.New("lost publication acknowledgement")
		}
		return version, err
	}}, f.clock)
	if _, err := m.RestoreState(t.Context(), f.claim, "uncertain-restore", 1, exported); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	result, err := m.RestoreState(t.Context(), f.claim, "uncertain-restore", 1, exported)
	if err != nil || !result.Replayed || result.Version != 2 {
		t.Fatal(result, err)
	}
}

func TestStateExportRestoreAndReplay(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("original"), withOutbox(outboxIntent())); err != nil {
		t.Fatal(err)
	}
	exported, err := f.manager.ExportState(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	base, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.manager.readSnapshot(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("changed"), func(_ context.Context, v View) (Transition, error) {
		return Transition{Data: json.RawMessage(`{"changed":true}`), Result: json.RawMessage(`null`), AlarmAt: v.AlarmAt}, nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := f.manager.RestoreState(t.Context(), f.claim, "restore-1", 2, exported)
	if err != nil || result.Version != 3 || result.Replayed {
		t.Fatal(result, err)
	}
	view, err := f.manager.Read(t.Context(), f.id)
	if err != nil || !reflect.DeepEqual(view.Data, exported.Data) {
		t.Fatal(view, err)
	}
	base, _, err = f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.manager.readSnapshot(t.Context(), base)
	if err != nil || !reflect.DeepEqual(before.Outbox, after.Outbox) {
		t.Fatal("outbox changed", err)
	}
	replay, err := f.manager.RestoreState(t.Context(), f.claim, "restore-1", 2, exported)
	if err != nil || !replay.Replayed || replay.Version != result.Version {
		t.Fatal(replay, err)
	}
	if _, err := f.manager.RestoreState(t.Context(), f.claim, "restore-2", 2, exported); !errors.Is(err, ErrRestoreObsolete) {
		t.Fatal(err)
	}
	if _, err := f.manager.RestoreState(t.Context(), f.claim, "restore-1", 3, exported); !errors.Is(err, ErrRequestConflict) {
		t.Fatal(err)
	}
}

func TestStateRestorePreservesExhaustedDelivery(t *testing.T) {
	for _, target := range []string{"alarm", "outbox"} {
		t.Run(target, func(t *testing.T) {
			f, _ := exhaustedRecoveryFixture(t, target)
			exported, err := f.manager.ExportState(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			claim, err := f.manager.Acquire(t.Context(), f.id, "restore-worker")
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.manager.Inspect(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.RestoreState(t.Context(), claim, "restore", exported.Version, exported); err != nil {
				t.Fatal(err)
			}
			after, err := f.manager.Inspect(t.Context(), f.id)
			if err != nil || before.Alarm.Attempts != after.Alarm.Attempts || before.Alarm.Exhausted != after.Alarm.Exhausted || before.Outbox.Attempts != after.Outbox.Attempts || before.Outbox.Exhausted != after.Outbox.Exhausted || before.Outbox.HeadID != after.Outbox.HeadID {
				t.Fatal("restore reset delivery", err)
			}
		})
	}
}

func TestStateRestoreRejectsInvalidAndStaleAuthority(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.ExportState(t.Context(), f.id); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("seed"), withOutbox()); err != nil {
		t.Fatal(err)
	}
	exported, err := f.manager.ExportState(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*StateExport){
		func(v *StateExport) { v.Data = json.RawMessage(`{"tampered":true}`) },
		func(v *StateExport) { v.Entity.Key = "other"; v.Checksum = exportChecksum(*v) },
		func(v *StateExport) { v.Format++; v.Checksum = exportChecksum(*v) },
	} {
		value := exported
		mutate(&value)
		if _, err := f.manager.RestoreState(t.Context(), f.claim, "invalid", 1, value); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Acquire(t.Context(), f.id, "new-owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.RestoreState(t.Context(), f.claim, "stale", 1, exported); !errors.Is(err, ErrStaleOwner) {
		t.Fatal(err)
	}
}
