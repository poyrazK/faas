// adr: 1027
package dbarchive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type memRemote struct {
	mu      sync.Mutex
	objects map[string][]byte
	fail    error
	puts    int
}

func (m *memRemote) Put(_ context.Context, key string, r io.ReadSeeker, size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	body, err := io.ReadAll(r)
	if err != nil || int64(len(body)) != size {
		return errors.New("short body")
	}
	m.objects[key] = body
	m.puts++
	return nil
}

func (m *memRemote) Get(_ context.Context, key string, w io.Writer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	body, ok := m.objects[key]
	if !ok {
		return ErrNotFound
	}
	_, err := w.Write(body)
	return err
}

func (m *memRemote) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	delete(m.objects, key)
	return nil
}

func (m *memRemote) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *memRemote) Sizes(_ context.Context, prefix string) (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, m.fail
	}
	sizes := map[string]int64{}
	for k, v := range m.objects {
		if strings.HasPrefix(k, prefix) {
			sizes[k] = int64(len(v))
		}
	}
	return sizes, nil
}

func (m *memRemote) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objects[key]
	return ok
}

func TestUploaderShipsOnceAndReportsLag(t *testing.T) {
	store := newStore(t)
	remote := &memRemote{objects: map[string][]byte{}}
	ctx := context.Background()
	for _, name := range []string{seg, "000000010000000000000002"} {
		if err := store.PutWAL(ctx, dbA, name, strings.NewReader(name), int64(len(name))); err != nil {
			t.Fatal(err)
		}
	}
	var pending int
	u := &Uploader{Store: store, Remote: remote, Prefix: "native", OnPending: func(n int, _ time.Duration) { pending = n }}

	remote.fail = errors.New("bucket down")
	if n, err := u.RunOnce(ctx); err == nil || n != 0 || pending != 2 {
		t.Fatalf("with the bucket down: shipped %d, err %v, pending %d", n, err, pending)
	}
	remote.fail = nil
	if n, err := u.RunOnce(ctx); err != nil || n != 2 || pending != 0 {
		t.Fatalf("upload pass: shipped %d, err %v, pending %d", n, err, pending)
	}
	if n, _ := u.RunOnce(ctx); n != 0 || remote.puts != 2 {
		t.Fatalf("a second pass re-uploaded (%d, puts %d)", n, remote.puts)
	}
	if got := remote.objects[WALKey("native", dbA, seg)]; string(got) != seg {
		t.Fatalf("remote object = %q", got)
	}
}

// A restore on another node, or after the local copy is pruned, reads the
// remote archive through the same store.
func TestTieredStoreFallsBackToRemote(t *testing.T) {
	remote := &memRemote{objects: map[string][]byte{WALKey("native", dbA, seg): []byte("from-remote")}}
	store := &TieredStore{LocalStore: newStore(t), Remote: remote, Prefix: "native"}
	ctx := context.Background()
	rc, size, err := store.OpenWAL(ctx, dbA, seg)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "from-remote" || size != int64(len(body)) {
		t.Fatalf("fetched %q (%d)", body, size)
	}
	// Nothing of a remote read is kept: no local copy, no temporary file.
	if _, err := os.Stat(filepath.Join(store.dir, dbA)); !os.IsNotExist(err) {
		t.Fatalf("a remote read left a local archive: %v", err)
	}
	if entries, _ := os.ReadDir(store.fetchDir()); len(entries) != 0 {
		t.Fatalf("temporary fetch files left behind: %v", entries)
	}
	if _, _, err := store.OpenWAL(ctx, dbA, "000000010000000000000009"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing everywhere = %v", err)
	}
}

// TestRestoreFromTheBucketAfterNodeLoss: a node with an empty disk serves
// a restore of another database entirely from the bucket.
func TestRestoreFromTheBucketAfterNodeLoss(t *testing.T) {
	for name, remote := range map[string]Remote{
		"mem":  &memRemote{objects: map[string][]byte{}},
		"file": &FileRemote{Dir: t.TempDir()},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			lost := newStore(t)
			first := putBackup(t, lost, dbA, 2, t0, true)
			second := putBackup(t, lost, dbA, 5, t0.Add(time.Hour), true)
			putFile(t, lost, dbA, segName(5), "wal-5")
			if _, err := (&Uploader{Store: lost, Remote: remote, Prefix: "native"}).RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			want, err := lost.OpenBackup(ctx, dbA, first.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantTar, _ := io.ReadAll(want)
			_ = want.Close()

			fresh := &TieredStore{LocalStore: newStore(t), Remote: remote, Prefix: "native"}
			all, err := fresh.ListAllBackups(ctx, dbA)
			if err != nil || len(all) != 2 || all[0].ID != first.ID || all[1].ID != second.ID {
				t.Fatalf("catalog from the bucket = %+v, %v", all, err)
			}
			dial := serveServer(t, &Server{Store: fresh, Backups: fresh})
			var got bytes.Buffer
			id, err := dial(Header{DatabaseID: dbB, RestoreSource: dbA}).GetBackup(t0.Add(30*time.Minute), &got)
			if err != nil || id != first.ID || !bytes.Equal(got.Bytes(), wantTar) {
				t.Fatalf("restore from the bucket = %q (%d bytes), %v", id, got.Len(), err)
			}
			var wal bytes.Buffer
			if err := dial(Header{DatabaseID: dbB, RestoreSource: dbA}).GetWAL(segName(5), &wal); err != nil || wal.String() != "wal-5" {
				t.Fatalf("wal from the bucket = %q, %v", wal.String(), err)
			}
			if _, err := os.Stat(filepath.Join(fresh.dir, dbA)); !os.IsNotExist(err) {
				t.Fatalf("remote reads were kept locally: %v", err)
			}
			// Local and bucket copies of one backup count once.
			both := &TieredStore{LocalStore: lost, Remote: remote, Prefix: "native"}
			if all, err := both.ListAllBackups(ctx, dbA); err != nil || len(all) != 2 {
				t.Fatalf("merged catalog = %+v, %v", all, err)
			}
		})
	}
}

func TestFileRemoteRefusesEscapingKeys(t *testing.T) {
	r := &FileRemote{Dir: t.TempDir()}
	for _, key := range []string{"", "/etc/passwd", "../x", "a/../../x", "a//b"} {
		if err := r.Put(context.Background(), key, strings.NewReader("x"), 1); err == nil {
			t.Errorf("key %q accepted", key)
		}
	}
	if err := r.Delete(context.Background(), "native/missing"); err != nil {
		t.Fatalf("deleting a missing key = %v", err)
	}
}

// Base backups leave the node too, data before manifest, so a remote
// manifest never names missing data.
func TestUploaderShipsBackupsDataFirst(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	m, err := store.PutBackup(ctx, dbA, bytes.NewReader(baseTar(t, "0/2000028", "000000010000000000000002", start, "rows")), start)
	if err != nil {
		t.Fatal(err)
	}
	remote := &orderedRemote{memRemote: memRemote{objects: map[string][]byte{}}}
	u := &Uploader{Store: store, Remote: remote, Prefix: "native"}
	if n, err := u.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("shipped %d, %v", n, err)
	}
	data, manifest := BaseKey("native", dbA, m.ID+".tar.zst"), BaseKey("native", dbA, m.ID+".json")
	if len(remote.order) != 2 || remote.order[0] != data || remote.order[1] != manifest {
		t.Fatalf("upload order = %v", remote.order)
	}
	if n, _ := u.RunOnce(ctx); n != 0 {
		t.Fatalf("second pass shipped %d", n)
	}
}

type orderedRemote struct {
	memRemote
	order []string
}

func (o *orderedRemote) Put(ctx context.Context, key string, r io.ReadSeeker, size int64) error {
	o.order = append(o.order, key)
	return o.memRemote.Put(ctx, key, r, size)
}
