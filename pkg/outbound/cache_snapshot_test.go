package outbound

import (
	"encoding/gob"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func snapshotTestHandler(t *testing.T) *Handler {
	t.Helper()
	cache, err := newOutboundResponseCache()
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{responseCache: cache, semanticIndex: newAISemanticIndex()}
}

// ADR-20261128150000: a stopping or handing-off generation writes its caches
// and the next one finds the same entries under the same keys.
func TestCacheSnapshotCarriesEntriesToTheNextGeneration(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	old := snapshotTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "https://api.example/v1/chat/completions", nil)
	key := old.responseCache.key("int-1", "app-1", 3, 60, req, []byte(`{"q":1}`))
	old.responseCache.put(&outboundCacheEntry{key: key, status: 200, header: http.Header{"Content-Type": {"application/json"}},
		body: []byte(`{"a":1}`), storedAt: now, expiresAt: now.Add(time.Minute), bytes: 64})
	expired := old.responseCache.key("int-1", "app-1", 3, 60, req, []byte(`{"q":2}`))
	old.responseCache.put(&outboundCacheEntry{key: expired, status: 200, body: []byte(`x`), storedAt: now.Add(-time.Hour),
		expiresAt: now.Add(-time.Second), bytes: 64})
	old.semanticIndex.add("int-1", "partition", key, []float32{1, 0})
	old.semanticIndex.add("int-1", "partition", expired, []float32{0, 1})

	if n, err := old.WriteCacheSnapshot(dir, now); err != nil || n != 1 {
		t.Fatalf("write = %d, %v; want the one live entry", n, err)
	}
	info, err := os.Stat(filepath.Join(dir, cacheSnapshotFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot = %v, %v; want a 0600 file (it holds provider responses and the key secret)", info, err)
	}

	next := snapshotTestHandler(t)
	if n, err := next.LoadCacheSnapshot(dir, now.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("load = %d, %v", n, err)
	}
	// The same request maps to the same key: the secret came along.
	if got := next.responseCache.key("int-1", "app-1", 3, 60, req, []byte(`{"q":1}`)); got != key {
		t.Fatal("the next generation computes a different key for the same request")
	}
	resp, ok := next.responseCache.get(key, now.Add(time.Second))
	if !ok {
		t.Fatal("cached response lost across the hand-over")
	}
	if body, _ := io.ReadAll(resp.Body); string(body) != `{"a":1}` || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("response = %q %v", body, resp.Header)
	}
	if found, _, ok := next.semanticIndex.nearest("int-1", "partition", []float32{1, 0}, 0.9); !ok || found != key {
		t.Fatalf("semantic entry = %q, %v; want it to point at the carried answer", found, ok)
	}
	if _, _, ok := next.semanticIndex.nearest("int-1", "partition", []float32{0, 1}, 0.9); ok {
		t.Fatal("a semantic entry whose answer expired was carried over")
	}
	if _, err := os.Stat(filepath.Join(dir, cacheSnapshotFile)); !os.IsNotExist(err) {
		t.Fatalf("snapshot still on disk after load: %v", err)
	}
}

func TestCacheSnapshotIgnoresStaleOrForeignSnapshots(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		written time.Time
		version int
	}{
		{"too old", now.Add(-time.Duration(api.OutboundCacheSnapshotMaxAgeSeconds+1) * time.Second), cacheSnapshotVersion},
		{"another version", now, cacheSnapshotVersion + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			old := snapshotTestHandler(t)
			old.responseCache.put(&outboundCacheEntry{key: "k", status: 200, storedAt: now, expiresAt: now.Add(time.Hour), bytes: 64})
			if _, err := old.WriteCacheSnapshot(dir, tc.written); err != nil {
				t.Fatal(err)
			}
			if tc.version != cacheSnapshotVersion {
				rewriteSnapshotVersion(t, dir, tc.version)
			}
			next := snapshotTestHandler(t)
			if n, err := next.LoadCacheSnapshot(dir, now); err != nil || n != 0 {
				t.Fatalf("load = %d, %v; want it ignored", n, err)
			}
			if _, err := os.Stat(filepath.Join(dir, cacheSnapshotFile)); !os.IsNotExist(err) {
				t.Fatalf("ignored snapshot left on disk: %v", err)
			}
		})
	}
	// No directory configured, or nothing written: nothing happens.
	h := snapshotTestHandler(t)
	if n, err := h.LoadCacheSnapshot("", now); n != 0 || err != nil {
		t.Fatalf("empty dir = %d, %v", n, err)
	}
	if n, err := h.LoadCacheSnapshot(t.TempDir(), now); n != 0 || err != nil {
		t.Fatalf("no snapshot = %d, %v", n, err)
	}
	// A corrupt snapshot is an error and is deleted.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, cacheSnapshotFile), []byte("not gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.LoadCacheSnapshot(dir, now); err == nil || !strings.Contains(err.Error(), "cache snapshot") {
		t.Fatalf("corrupt snapshot err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, cacheSnapshotFile)); !os.IsNotExist(err) {
		t.Fatalf("corrupt snapshot left on disk: %v", err)
	}
}

func rewriteSnapshotVersion(t *testing.T, dir string, version int) {
	t.Helper()
	h := snapshotTestHandler(t)
	snapshot := cacheSnapshot{Version: version, WrittenAt: time.Now()}
	snapshot.Secret, snapshot.Responses = h.responseCache.export(time.Now())
	file, err := os.Create(filepath.Join(dir, cacheSnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := gob.NewEncoder(file).Encode(&snapshot); err != nil {
		t.Fatal(err)
	}
}

// EndAICalls ends calls in flight through their deadline path.
func TestEndAICallsEndsCallsInFlight(t *testing.T) {
	h := &Handler{}
	ended := make(chan struct{})
	call := &aiCallContext{cancel: func(error) { close(ended) }}
	stop := h.aiDrain.track(call)
	defer stop()
	h.EndAICalls()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("EndAICalls did not end the call in flight")
	}
}
