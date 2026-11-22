package volbackup_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

// adr: 20261122132055 §4 — a volume backup stores each data chunk once per
// account, skips zeros, reads only changed chunks when told which, and
// restores a byte-identical sparse volume, refusing a corrupted chunk.

const (
	account = "11111111-1111-1111-1111-111111111111"
	sandbox = "22222222-2222-2222-2222-222222222222"
)

func newStore(t *testing.T) storage.StorageBackend {
	t.Helper()
	s, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeAt(t *testing.T, path string, off int64, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteAt(b, off); err != nil {
		t.Fatal(err)
	}
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	dir := t.TempDir()
	vol := filepath.Join(dir, "vol.ext4")
	const size = 5*volbackup.ChunkBytes + 1000 // a short last chunk
	if err := os.WriteFile(vol, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(vol, size); err != nil {
		t.Fatal(err)
	}
	writeAt(t, vol, 10, randomBytes(t, 5000))
	writeAt(t, vol, 3*volbackup.ChunkBytes, randomBytes(t, 100))
	writeAt(t, vol, size-10, []byte("tail-bytes"))

	first, st, err := volbackup.Backup(ctx, store, account, sandbox, "b1", vol, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Chunks) != 6 || first.Chunks[1] != "" || first.Chunks[0] == "" || st.ChunksRead != 6 || st.ChunksUploaded != 3 {
		t.Fatalf("first backup = %v, %+v", first.Chunks, st)
	}

	// Change one chunk; the hint names it, so only it is read and put.
	writeAt(t, vol, 4*volbackup.ChunkBytes+7, []byte("changed"))
	changed := []storage.Range{{Start: 4*volbackup.ChunkBytes + 7, End: 4*volbackup.ChunkBytes + 14}}
	second, st, err := volbackup.Backup(ctx, store, account, sandbox, "b2", vol, &first, changed)
	if err != nil {
		t.Fatal(err)
	}
	if st.ChunksRead != 1 || st.ChunksUploaded != 1 || second.Chunks[0] != first.Chunks[0] || second.Chunks[4] == "" {
		t.Fatalf("incremental backup = %v, %+v", second.Chunks, st)
	}

	m, err := volbackup.ReadManifest(ctx, store, account, sandbox, "b2")
	if err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(dir, "restored", "vol.ext4")
	if err := volbackup.Restore(ctx, store, account, m, restored); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(vol)
	got, err := os.ReadFile(restored)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("restored volume differs (%v)", err)
	}
	// The first backup still restores the volume as it was.
	old, err := volbackup.ReadManifest(ctx, store, account, sandbox, "b1")
	if err != nil {
		t.Fatal(err)
	}
	if err := volbackup.Restore(ctx, store, account, old, restored); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(restored); bytes.Contains(got, []byte("changed")) {
		t.Fatal("the first backup restored a later write")
	}
}

func TestBackupOfAGrownVolumeRereadsItsOldLastChunk(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	vol := filepath.Join(t.TempDir(), "vol.ext4")
	if err := os.WriteFile(vol, randomBytes(t, volbackup.ChunkBytes+10), 0o600); err != nil {
		t.Fatal(err)
	}
	first, _, err := volbackup.Backup(ctx, store, account, sandbox, "b1", vol, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(vol, 3*volbackup.ChunkBytes); err != nil {
		t.Fatal(err)
	}
	second, st, err := volbackup.Backup(ctx, store, account, sandbox, "b2", vol, &first, []storage.Range{})
	if err != nil {
		t.Fatal(err)
	}
	if st.ChunksRead != 2 || second.Chunks[0] != first.Chunks[0] || second.Chunks[1] == first.Chunks[1] {
		t.Fatalf("grown backup read %d chunks: %v vs %v", st.ChunksRead, second.Chunks, first.Chunks)
	}
}

func TestRestoreRefusesACorruptChunk(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	dir := t.TempDir()
	vol := filepath.Join(dir, "vol.ext4")
	if err := os.WriteFile(vol, randomBytes(t, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _, err := volbackup.Backup(ctx, store, account, sandbox, "b1", vol, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	other := randomBytes(t, 1000)
	if err := os.WriteFile(vol, other, 0o600); err != nil {
		t.Fatal(err)
	}
	m2, _, err := volbackup.Backup(ctx, store, account, sandbox, "b2", vol, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Swap the first chunk's object for the second's contents.
	rc, err := store.Get(ctx, volbackup.ChunkKey(account, m2.Chunks[0]))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rc)
	_ = rc.Close()
	if err := store.Put(ctx, volbackup.ChunkKey(account, m.Chunks[0]), &buf); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "restored.ext4")
	if err := volbackup.Restore(ctx, store, account, m, dst); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("restore of a corrupt chunk = %v", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("a failed restore left a volume")
	}
}

func TestBackupRefusesUnsafeIDs(t *testing.T) {
	store := newStore(t)
	vol := filepath.Join(t.TempDir(), "vol.ext4")
	if err := os.WriteFile(vol, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][3]string{{"../a", sandbox, "b"}, {account, "a/b", "b"}, {account, sandbox, ""}} {
		if _, _, err := volbackup.Backup(context.Background(), store, ids[0], ids[1], ids[2], vol, nil, nil); err == nil {
			t.Fatalf("Backup(%v) succeeded", ids)
		}
	}
}
