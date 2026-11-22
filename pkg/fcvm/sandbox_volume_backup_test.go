package fcvm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// adr: 20261122132055 §4 — a backup reads a clone of the volume; the next
// compares with the clone the last one read; a node without the volume
// restores it from a backup and backs up incrementally from there.

const (
	backupAccount = "11111111-1111-1111-1111-111111111111"
	backupSandbox = "22222222-2222-2222-2222-222222222222"
)

func volumeFixture(t *testing.T) (*SandboxVolumes, storage.StorageBackend, string, func(int64, string)) {
	t.Helper()
	store, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v := NewSandboxVolumes(t.TempDir())
	v.run = (&volumeRuns{}).run
	path, _, err := v.Ensure(context.Background(), backupAccount, backupSandbox, 1)
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
	return v, store, path, write
}

func backupFiles(t *testing.T, path, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(path + pattern)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSandboxVolumeBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	v, store, path, write := volumeFixture(t)
	write(0, "first")
	b1, err := v.Backup(ctx, store, backupAccount, backupSandbox, "b1")
	if err != nil || b1.Incremental || b1.Stats.ChunksUploaded != 1 {
		t.Fatalf("first backup = %+v, %v", b1, err)
	}
	write(600<<20, "second")
	b2, err := v.Backup(ctx, store, backupAccount, backupSandbox, "b2")
	if err != nil || b2.Stats.ChunksUploaded != 1 {
		t.Fatalf("second backup = %+v, %v", b2, err)
	}
	if pending := backupFiles(t, path, volumeBackupPendingInfix+"*"); len(pending) != 0 {
		t.Fatalf("the backup left its clone: %v", pending)
	}
	if bases := backupFiles(t, path, volumeBackupBaseInfix+"*"); len(bases) != 1 || bases[0] != path+volumeBackupBaseInfix+"b2" {
		t.Fatalf("bases = %v", bases)
	}
	if m, ok := v.LastBackup(backupAccount, backupSandbox); !ok || m.ID != "b2" {
		t.Fatalf("last backup = %v %v", m.ID, ok)
	}
	want, _ := os.ReadFile(path)
	if err := v.Remove(backupAccount, backupSandbox); err != nil {
		t.Fatal(err)
	}
	if left := backupFiles(t, path, "*"); len(left) != 0 || v.Exists(backupAccount, backupSandbox) {
		t.Fatalf("Remove left %v", left)
	}
	restored, err := v.RestoreBackup(ctx, store, backupAccount, backupSandbox, "b2")
	if err != nil || restored != path {
		t.Fatalf("restore = %q, %v", restored, err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
		t.Fatal("the restored volume differs")
	}
	write(900<<20, "third")
	b3, err := v.Backup(ctx, store, backupAccount, backupSandbox, "b3")
	if err != nil || b3.Stats.ChunksUploaded != 1 || b3.Manifest.Chunks[0] != b2.Manifest.Chunks[0] {
		t.Fatalf("backup after restore = %+v, %v", b3, err)
	}
}

// A pause's clone uploads in the background, possibly after a restart and
// after a later backup; the node keeps the newer one as its base.
func TestSandboxVolumePendingBackupSurvivesAndKeepsTheNewest(t *testing.T) {
	ctx := context.Background()
	v, store, path, write := volumeFixture(t)
	write(0, "at-pause")
	if _, err := v.PrepareBackup(backupAccount, backupSandbox, "pause"); err != nil {
		t.Fatal(err)
	}
	// A restart forgets the value; the clone is found again.
	pending := v.PendingBackups()
	if len(pending) != 1 || pending[0].ID != "pause" || pending[0].AccountID != backupAccount || pending[0].SandboxID != backupSandbox {
		t.Fatalf("pending = %+v", pending)
	}
	write(0, "later---")
	if _, err := v.Backup(ctx, store, backupAccount, backupSandbox, "later"); err != nil {
		t.Fatal(err)
	}
	b, err := pending[0].Upload(ctx, store)
	if err != nil || b.Manifest.ID != "pause" {
		t.Fatalf("pending upload = %+v, %v", b, err)
	}
	if m, ok := v.LastBackup(backupAccount, backupSandbox); !ok || m.ID != "later" {
		t.Fatalf("the older pause backup replaced the newer base: %v", m.ID)
	}
	if left := backupFiles(t, path, volumeBackupPendingInfix+"*"); len(left) != 0 {
		t.Fatalf("pending clones left: %v", left)
	}
	// The pause backup restores the volume as the pause left it.
	if _, err := v.RestoreBackup(ctx, store, backupAccount, backupSandbox, "pause"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.HasPrefix(got, []byte("at-pause")) {
		t.Fatalf("restored %q", got[:8])
	}
}
