package s3gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

// ADR-1347: older sources stay on single-entry reads; advertised pages retain
// authentication and the same applied-only replica confirmation boundary.
func TestDiskReplicationEntryBatchesAndLegacySource(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "batch"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			store, h, directory, _, _ := replicaDiskFixture(t)
			for _, key := range []string{"first", "second", "third"} {
				if _, err := store.Put(t.Context(), "test-bucket", key, strings.NewReader("data"), 4, objectnode.PutOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			var batches, singles atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Query().Get(objectnode.ReplicationQuery) {
				case "status":
					if legacy {
						response := httptest.NewRecorder()
						h.ServeHTTP(response, r)
						response.Header().Del(objectnode.ReplicationBatchHeader)
						for key, values := range response.Header() {
							w.Header()[key] = values
						}
						w.WriteHeader(response.Code)
						_, _ = w.Write(response.Body.Bytes())
						return
					}
				case "entries":
					batches.Add(1)
				case "entry":
					singles.Add(1)
				}
				h.ServeHTTP(w, r)
			}))
			defer server.Close()
			source, err := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", diskTestCredential, diskTestCredential)
			if err != nil {
				t.Fatal(err)
			}
			p, err := objectnode.CatchUpReplica(t.Context(), directory, source, objectnode.CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes, WorkBytesPerSecond: api.ObjectNodeReplicationDefaultWorkBytes, WorkEntriesPerSecond: api.ObjectNodeReplicationDefaultWorkEntries})
			if err != nil || !p.CaughtUp || p.EntriesApplied != 3 || p.ConfirmedSequence == nil || *p.ConfirmedSequence != 3 || legacy && (batches.Load() != 0 || singles.Load() != 3) || !legacy && (batches.Load() != 1 || singles.Load() != 0) {
				t.Fatal("wrong fetch path or premature confirmation", p, batches.Load(), singles.Load(), err)
			}
		})
	}
}

func TestDiskReplicationEntryBatchValidation(t *testing.T) {
	store, h, _, server, source := replicaDiskFixture(t)
	if _, err := store.Put(t.Context(), "test-bucket", "key", strings.NewReader("data"), 4, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	head, err := source.Head(t.Context())
	if err != nil || source.EntryBatchLimit() != api.ObjectNodeReplicationBatch {
		t.Fatal("batch capability was not advertised", err)
	}
	base := url.Values{objectnode.ReplicationQuery: {"entries"}, "stream": {head.StreamID}, "sequence": {"1"}, "limit": {"1"}}
	for _, change := range []url.Values{{"sequence": {"01"}}, {"limit": {"0"}}, {"limit": {"33"}}, {"limit": {"1", "2"}}, {"extra": {"1"}}} {
		query := make(url.Values)
		for key, values := range base {
			query[key] = append([]string(nil), values...)
		}
		for key, values := range change {
			query[key] = values
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, signedInventoryRequest(t, http.MethodGet, "/?"+query.Encode()))
		if w.Code != http.StatusBadRequest || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("malformed batch query accepted", query, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?"+base.Encode(), nil))
	if w.Code != http.StatusForbidden {
		t.Fatal("unauthenticated journal page accepted", w.Code)
	}
	wrong, _ := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", diskTestCredential, "wrong-secret")
	if _, err := wrong.Entries(t.Context(), head.StreamID, 1, 1); err == nil {
		t.Fatal("wrong signature read journal data")
	}
}

func TestDiskReplicationEntryBatchesBoundedAndIndependent(t *testing.T) {
	store, h, _, _, source := replicaDiskFixture(t)
	o, err := store.Put(t.Context(), "test-bucket", "key", strings.NewReader("data"), 4, objectnode.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	head, err := source.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{objectnode.ReplicationQuery: {"entries"}, "stream": {head.StreamID}, "sequence": {"1"}, "limit": {"1"}}
	path := "/?" + query.Encode()
	release := make(chan struct{})
	var released sync.Once
	resume := func() { released.Do(func() { close(release) }) }
	defer resume()
	var workers sync.WaitGroup
	for range api.ObjectNodeReplicationMaxEntryBatches {
		response := &blockedReplicationResponse{header: make(http.Header), started: make(chan struct{}), release: release, fail: true}
		request := signedInventoryRequest(t, http.MethodGet, path)
		workers.Go(func() { h.ServeHTTP(response, request) })
		select {
		case <-response.started:
		case <-time.After(5 * time.Second):
			t.Fatal("authenticated journal page never started")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedInventoryRequest(t, http.MethodGet, path))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("excess page admitted", w.Code)
	}
	if _, err := source.Head(t.Context()); err != nil {
		t.Fatal("journal responses blocked status", err)
	}
	body, err := source.Blob(t.Context(), head.StreamID, o.SHA256, 0, 4)
	if err != nil {
		t.Fatal("journal responses consumed payload slots", err)
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
	resume()
	workers.Wait()
	if len(h.metrics.replication.entryBatches) != 0 {
		t.Fatal("failed response leaked page slots")
	}
	if entries, err := source.Entries(t.Context(), head.StreamID, 1, 1); err != nil || len(entries) != 1 {
		t.Fatal("journal pool did not recover", err)
	}
}
