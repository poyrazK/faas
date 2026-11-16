package storage

// adr: 20261115180000

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

type recordingDeferrer struct{ keys []string }

func (r *recordingDeferrer) DeferDelete(_ context.Context, key string) error {
	r.keys = append(r.keys, key)
	return nil
}

// A remote store behind the cache and the local-prefix router records the
// full key; the cache still evicts and the local route still deletes.
func TestDeferDeletes_RemoteRecordsLocalDeletes(t *testing.T) {
	var remoteDeletes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			remoteDeletes.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	remote, err := NewOCIRegistryStorageBackend(WithRegistry(srv.URL+"/acme"), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	local, err := NewLocalStorageBackend(filepath.Join(root, "kernel"))
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewPrefixRouter(map[string]StorageBackend{"kernel/": local}, remote)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewLocalCacheBackend(router, filepath.Join(root, "cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	d := &recordingDeferrer{}
	n, err := DeferDeletes(cache, d)
	if err != nil || n != 1 {
		t.Fatalf("DeferDeletes = %d, %v; want the one remote store", n, err)
	}

	ctx := context.Background()
	if err := cache.Delete(ctx, "snap/0b8f5a8e-7c1e-4f3a-9d2b-1a2b3c4d5e6f/mem"); err != nil {
		t.Fatalf("deferred delete: %v", err)
	}
	if len(d.keys) != 1 || d.keys[0] != "snap/0b8f5a8e-7c1e-4f3a-9d2b-1a2b3c4d5e6f/mem" || remoteDeletes.Load() != 0 {
		t.Fatalf("recorded %v, remote deletes %d; want the full key recorded and nothing deleted", d.keys, remoteDeletes.Load())
	}
	if err := cache.Put(ctx, "kernel/1.0", bytes.NewReader([]byte("k"))); err != nil {
		t.Fatal(err)
	}
	if err := cache.Delete(ctx, "kernel/1.0"); err != nil {
		t.Fatalf("local delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "kernel", "1.0")); !os.IsNotExist(err) {
		t.Fatalf("local route file still there: %v", err)
	}
	if len(d.keys) != 1 {
		t.Fatalf("a local route delete was recorded: %v", d.keys)
	}
}

func TestDeferDeletes_Refusals(t *testing.T) {
	remote, err := NewOCIRegistryStorageBackend(WithRegistry("https://registry.invalid/acme"))
	if err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if n, err := DeferDeletes(local, &recordingDeferrer{}); err != nil || n != 0 {
		t.Fatalf("local backend = %d, %v; want nothing to defer", n, err)
	}
	stripped, err := NewPrefixRouter(map[string]StorageBackend{"apps/": remote}, local)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DeferDeletes(stripped, &recordingDeferrer{}); err == nil {
		t.Fatal("a prefix route to the shared store was accepted; it would record stripped keys")
	}
	if _, err := DeferDeletes(unknownBackend{}, &recordingDeferrer{}); err == nil {
		t.Fatal("an unknown backend was accepted")
	}
	if _, err := DeferDeletes(remote, nil); err == nil {
		t.Fatal("a nil deferrer was accepted")
	}
}

type unknownBackend struct{}

func (unknownBackend) Put(context.Context, string, io.Reader) error       { return nil }
func (unknownBackend) Get(context.Context, string) (io.ReadCloser, error) { return nil, ErrNotFound }
func (unknownBackend) Delete(context.Context, string) error               { return nil }
