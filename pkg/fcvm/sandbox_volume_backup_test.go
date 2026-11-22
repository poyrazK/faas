package fcvm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// adr: 20261122132055 §4 — a backup reads a clone of the volume; the next
// compares with the clone the last one read; a node without the volume
// restores it from a backup and backs up incrementally from there.
func TestSandboxVolumeBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v := NewSandboxVolumes(t.TempDir())
	v.run = (&volumeRuns{}).run
	const account, sandbox = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	path, _, err := v.Ensure(ctx, account, sandbox, 1)
	if err != nil {
		t.Fatal(err)
	}
	write := func(off int64, s string) {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteAt([]byte(s), off); err != nil {
			t.Fatal(err)
		}
	}
	write(0, "first")
	b1, err := v.Backup(ctx, store, account, sandbox, "b1")
	if err != nil || b1.Incremental || b1.Stats.ChunksUploaded != 1 {
		t.Fatalf("first backup = %+v, %v", b1, err)
	}
	write(600<<20, "second")
	b2, err := v.Backup(ctx, store, account, sandbox, "b2")
	if err != nil || b2.Stats.ChunksUploaded != 1 {
		t.Fatalf("second backup = %+v, %v", b2, err)
	}
	if _, err := os.Stat(path + volumeBackupNextSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the backup left its clone: %v", err)
	}
	want, _ := os.ReadFile(path)
	if err := v.Remove(account, sandbox); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + volumeBackupBaseSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left the backup clone: %v", err)
	}
	restored, err := v.RestoreBackup(ctx, store, account, sandbox, "b2")
	if err != nil || restored != path {
		t.Fatalf("restore = %q, %v", restored, err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
		t.Fatal("the restored volume differs")
	}
	write(900<<20, "third")
	b3, err := v.Backup(ctx, store, account, sandbox, "b3")
	if err != nil || b3.Stats.ChunksUploaded != 1 || b3.Manifest.Chunks[0] != b2.Manifest.Chunks[0] {
		t.Fatalf("backup after restore = %+v, %v", b3, err)
	}
}
