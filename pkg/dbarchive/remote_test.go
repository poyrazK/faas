// adr: 1027
package dbarchive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
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
	// Cached locally and marked uploaded: no second download, no re-upload.
	remote.objects = map[string][]byte{}
	if rc, _, err := store.OpenWAL(ctx, dbA, seg); err != nil {
		t.Fatalf("cached copy: %v", err)
	} else {
		_ = rc.Close()
	}
	if n, _ := (&Uploader{Store: store.LocalStore, Remote: remote, Prefix: "native"}).RunOnce(ctx); n != 0 {
		t.Fatal("a fetched segment was uploaded again")
	}
	if _, _, err := store.OpenWAL(ctx, dbA, "000000010000000000000009"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing everywhere = %v", err)
	}
	if entries, _ := os.ReadDir(store.dir + "/" + dbA + "/wal"); len(entries) != 1 {
		t.Fatalf("temporary fetch files left behind: %v", entries)
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
