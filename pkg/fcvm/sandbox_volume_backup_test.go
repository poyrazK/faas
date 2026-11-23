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

// A pause's backup is the node's copy before its upload finishes, and after
// it is given up: a resume on the node uses its own volume
// (ADR-20261122132055 §4).
func TestSandboxVolumeTookBackupBeforeUpload(t *testing.T) {
	v, _, path, write := volumeFixture(t)
	write(0, "at-pause")
	if v.TookBackup(backupAccount, backupSandbox, "pause") {
		t.Fatal("a backup never taken is the node's")
	}
	p, err := v.PrepareBackup(backupAccount, backupSandbox, "pause")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.LastBackup(backupAccount, backupSandbox); ok {
		t.Fatal("an unuploaded backup became the last one")
	}
	if !v.TookBackup(backupAccount, backupSandbox, "pause") {
		t.Fatal("the pause backup is not the node's before its upload")
	}
	p.Discard()
	if !v.TookBackup(backupAccount, backupSandbox, "pause") || v.TookBackup(backupAccount, backupSandbox, "other") ||
		v.TookBackup(backupAccount, backupSandbox, "") {
		t.Fatal("a given-up pause backup is not the node's, or another backup is")
	}
	if err := v.Remove(backupAccount, backupSandbox); err != nil {
		t.Fatal(err)
	}
	if v.TookBackup(backupAccount, backupSandbox, "pause") || len(backupFiles(t, path, ".backup*")) != 0 {
		t.Fatal("a removed volume left its backup mark")
	}
}

// A fork on the node that holds its source's backup clones it locally, before
// the upload finishes and after it; elsewhere it restores from the store.
func TestSandboxVolumeForkClonesTheLocalBackup(t *testing.T) {
	ctx := context.Background()
	v, store, _, write := volumeFixture(t)
	const fork, later = "33333333-3333-3333-3333-333333333333", "44444444-4444-4444-4444-444444444444"
	write(0, "at-pause")
	p, err := v.PrepareBackup(backupAccount, backupSandbox, "pause")
	if err != nil {
		t.Fatal(err)
	}
	write(0, "changed!")
	readsPause := func(name, path string) {
		t.Helper()
		got, _ := os.ReadFile(path)
		if !bytes.HasPrefix(got, []byte("at-pause")) {
			t.Fatalf("%s reads %q", name, got[:8])
		}
	}
	// Nothing is in the store yet: the pending clone is the only copy.
	forkPath, err := v.RestoreBackupFrom(ctx, store, backupAccount, backupSandbox, fork, "pause")
	if err != nil {
		t.Fatalf("fork before upload: %v", err)
	}
	readsPause("fork before upload", forkPath)
	if _, err := p.Upload(ctx, store); err != nil {
		t.Fatal(err)
	}
	// After the upload the base is the local copy.
	laterPath, err := v.RestoreBackupFrom(ctx, store, backupAccount, backupSandbox, later, "pause")
	if err != nil {
		t.Fatalf("fork after upload: %v", err)
	}
	readsPause("fork after upload", laterPath)
	// A node without the source's volume reads the store.
	other := NewSandboxVolumes(t.TempDir())
	other.run = (&volumeRuns{}).run
	otherPath, err := other.RestoreBackupFrom(ctx, store, backupAccount, backupSandbox, fork, "pause")
	if err != nil {
		t.Fatalf("fork elsewhere: %v", err)
	}
	readsPause("fork elsewhere", otherPath)
	// A backup the node does not hold and the store lacks fails.
	if _, err := other.RestoreBackupFrom(ctx, store, backupAccount, backupSandbox, later, "missing"); err == nil {
		t.Fatal("a missing backup restored")
	}
}
