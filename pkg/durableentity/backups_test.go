// adr: 852
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBackupUncertainWriteIsRecoveredWithoutReplacingSlot(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("seed"), increment); err != nil {
		t.Fatal(err)
	}
	lost := false
	m := openManager(t, wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		version, err := f.store.Put(ctx, key, body, etag)
		if err == nil && strings.HasPrefix(key, backupPrefix(f.id)) && !lost {
			lost = true
			return "", errors.New("lost acknowledgement")
		}
		return version, err
	}}, f.clock)
	if _, err := m.BackupState(t.Context(), f.id); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	info, err := m.BackupState(t.Context(), f.id)
	if err != nil || info.Version != 1 || !lost {
		t.Fatal(info, err)
	}
}

func TestBackupReadRejectsCorruptionAndOtherScope(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("seed"), increment); err != nil {
		t.Fatal(err)
	}
	info, err := f.manager.BackupState(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	other := f.id
	other.TenantID = "other"
	if _, err := f.manager.ReadBackup(t.Context(), other, info.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	key := backupPrefix(f.id) + info.ID + ".json"
	f.store.mu.Lock()
	obj := f.store.objects[key]
	obj.body = []byte(`{"captured_at":"bad"}`)
	f.store.objects[key] = obj
	f.store.mu.Unlock()
	if _, err := f.manager.ReadBackup(t.Context(), f.id, info.ID); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	if _, err := f.manager.PruneBackups(t.Context(), f.id); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}

func TestBackupsAreImmutableHourlyAndRetainedSeparately(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("seed"), increment); err != nil {
		t.Fatal(err)
	}
	base, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.manager.BackupState(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("change"), increment); err != nil {
		t.Fatal(err)
	}
	second, err := f.manager.BackupState(t.Context(), f.id)
	if err != nil || second != first {
		t.Fatal("hourly capture replaced", second, err)
	}
	read, err := f.manager.ReadBackup(t.Context(), f.id, first.ID)
	if err != nil || read.Export.Version != 1 {
		t.Fatal(read, err)
	}
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(int64(api.DurableEntityBackupRetention + api.DurableEntityBackupInterval))
	if _, err := f.manager.PruneBackups(t.Context(), f.id); !errors.Is(err, ErrNotFound) {
		t.Fatal("pruned without a current backup", err)
	}
	if _, err := f.manager.BackupState(t.Context(), f.id); err != nil {
		t.Fatal(err)
	}
	if deleted, err := f.manager.PruneBackups(t.Context(), f.id); err != nil || deleted != 1 {
		t.Fatal(deleted, err)
	}
	if _, err := f.manager.ReadBackup(t.Context(), f.id, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	page, err := f.manager.ListBackups(t.Context(), f.id, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Version != 2 {
		t.Fatal(page, err)
	}
	current, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil || current.Version != 2 || current.Generation != base.Generation {
		t.Fatal("backups changed execution history", current, err)
	}
}

func TestBackupScanAllowlistAndPreviewNeverChangeLiveState(t *testing.T) {
	f, _ := exhaustedRecoveryFixture(t, "outbox")
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
	if page, err := f.manager.ScanBackups(t.Context(), "", func(ID) bool { return false }); err != nil || page.Captured != 0 {
		t.Fatal(page, err)
	}
	if page, err := f.manager.ScanBackups(t.Context(), "", func(id ID) bool { return id == f.id }); err != nil || page.Captured != 1 || page.Failed != 0 {
		t.Fatal(page, err)
	}
	preview, err := f.manager.PreviewRestore(t.Context(), f.id, exported.Version+1, exported)
	if err != nil || preview.ExpectedVersionMatches || preview.Compatibility != "unverified" || !preview.OutboxExhausted || preview.OutboxPending != 2 {
		t.Fatal(preview, err)
	}
	afterBase, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil || !reflect.DeepEqual(base, afterBase) {
		t.Fatal("backup or preview wrote manifest", err)
	}
	after, err := f.manager.readSnapshot(t.Context(), afterBase)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("backup or preview changed snapshot", err)
	}
	for _, name := range []string{"../manifest", "20261007T123000Z", "20261007T120000Z/extra"} {
		if _, err := f.manager.ReadBackup(t.Context(), f.id, name); !errors.Is(err, ErrInvalid) {
			t.Fatal(name, err)
		}
	}
}

func TestPreviewSchemaRelationIsNotCompatibility(t *testing.T) {
	if version := applicationSchema(json.RawMessage(`{"schema_version":2,"data":{}}`)); version == nil || *version != 2 {
		t.Fatal(version)
	}
	for _, data := range []string{`{}`, `{"schema_version":0,"data":{}}`, `{"schema_version":2,"data":{},"extra":1}`} {
		if applicationSchema(json.RawMessage(data)) != nil {
			t.Fatal(data)
		}
	}
	if _, valid := backupTime(time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC).Format(backupTimeFormat)); !valid {
		t.Fatal("valid UTC slot rejected")
	}
}
