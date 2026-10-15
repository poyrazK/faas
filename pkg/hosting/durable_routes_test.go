package hosting

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type routeObjectFixture struct {
	mu               sync.Mutex
	data             []byte
	etag             string
	writes           int
	blockGeneration  int64
	blocked, release chan struct{}
	blockOnce        sync.Once
}

func routeBackend(t *testing.T) (*R2, *routeObjectFixture) {
	t.Helper()
	f := &routeObjectFixture{blocked: make(chan struct{}), release: make(chan struct{})}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			f.mu.Lock()
			data, etag := append([]byte(nil), f.data...), f.etag
			f.mu.Unlock()
			if etag == "" {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(404)
				_, _ = io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
				return
			}
			w.Header().Set("ETag", etag)
			_, _ = w.Write(data)
			return
		}
		if r.Method != http.MethodPut {
			t.Errorf("unexpected route method %s", r.Method)
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var route Route
		if err := json.Unmarshal(data, &route); err != nil {
			t.Error(err)
		}
		if route.Generation == f.blockGeneration {
			f.blockOnce.Do(func() { close(f.blocked) })
			<-f.release
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("If-Match") == "" && r.Header.Get("If-None-Match") != "*" {
			t.Error("route PUT was not conditional")
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("If-None-Match") == "*" && f.etag != "" || r.Header.Get("If-Match") != "" && r.Header.Get("If-Match") != f.etag {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(412)
			_, _ = io.WriteString(w, "<Error><Code>PreconditionFailed</Code></Error>")
			return
		}
		f.data = data
		f.etag = fmt.Sprintf("\"%x\"", sha256.Sum256(data))
		f.writes++
		w.Header().Set("ETag", f.etag)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		select {
		case <-f.release:
		default:
			close(f.release)
		}
	})
	backend, err := NewR2(server.URL, "bucket", "fixture", "fixture-secret")
	if err != nil {
		t.Fatal(err)
	}
	opts := backend.client.Options()
	opts.HTTPClient = server.Client()
	backend.client = s3.New(opts)
	return backend, f
}

func routeSite(origin string) (api.HostingSite, []api.HostingRelease) {
	id, release := uuid.NewString(), uuid.NewString()
	return api.HostingSite{ID: id, Hostname: id + ".hosting.example", OriginID: origin, Status: "active", Revision: 1, DurableRouting: true, ActiveReleaseID: release}, []api.HostingRelease{{ID: release, SiteID: id, State: "ready"}}
}

func TestDurableRouteRetriesDoNotWriteUnchangedHeads(t *testing.T) {
	// adr: 202610151515 — same-generation repair is a read, not a heartbeat PUT.
	backend, f := routeBackend(t)
	site, releases := routeSite(backend.ID())
	route := NewDurableRoute(site, releases, 1)
	if err := backend.Put(t.Context(), route); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := backend.Put(t.Context(), route); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if writes != 1 {
		t.Fatalf("unchanged repair performed %d writes", writes)
	}
	conflict := route
	conflict.Enabled = false
	if err := backend.Put(t.Context(), conflict); !errors.Is(err, ErrStaleRoute) {
		t.Fatalf("same generation changed bytes: %v", err)
	}
}

func TestDurableRouteDelayedWritersCannotReplaceNewerHeads(t *testing.T) {
	// Both negative reads and existing ETags must fence delayed publications.
	for _, exists := range []bool{false, true} {
		for _, deleted := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing=%t/deleted=%t", exists, deleted), func(t *testing.T) {
				backend, f := routeBackend(t)
				site, releases := routeSite(backend.ID())
				if exists {
					if err := backend.Put(t.Context(), NewDurableRoute(site, releases, 1)); err != nil {
						t.Fatal(err)
					}
				}
				f.blockGeneration = 2
				done := make(chan error, 1)
				go func() { done <- backend.Put(t.Context(), NewDurableRoute(site, releases, 2)) }()
				select {
				case <-f.blocked:
				case <-time.After(5 * time.Second):
					t.Fatal("older write did not reach conditional PUT")
				}
				newer := site
				newer.Revision++
				if deleted {
					newer.Status = "deleting"
					newer.ActiveReleaseID = ""
				}
				if err := backend.Put(t.Context(), NewDurableRoute(newer, releases, 3)); err != nil {
					t.Fatal(err)
				}
				close(f.release)
				select {
				case err := <-done:
					if !errors.Is(err, ErrStaleRoute) {
						t.Fatalf("late publisher: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("older writer did not finish")
				}
				f.mu.Lock()
				var head Route
				err := json.Unmarshal(f.data, &head)
				f.mu.Unlock()
				if err != nil || head.Generation != 3 || head.Deleted != deleted {
					t.Fatalf("newer head was overwritten: %+v %v", head, err)
				}
				if deleted {
					if err := backend.Put(t.Context(), NewDurableRoute(site, releases, 4)); !errors.Is(err, ErrStaleRoute) {
						t.Fatalf("retained deletion was resurrected: %v", err)
					}
				}
			})
		}
	}
}

func TestDurableRouteRejectsMalformedOrForeignHeads(t *testing.T) {
	backend, f := routeBackend(t)
	site, releases := routeSite(backend.ID())
	route := NewDurableRoute(site, releases, 1)
	foreign := route
	foreign.OriginID = strings.Repeat("a", 64)
	if err := backend.Put(t.Context(), foreign); err == nil {
		t.Fatal("accepted a different origin")
	}
	f.mu.Lock()
	f.data = []byte("null")
	f.etag = "\"bad\""
	f.mu.Unlock()
	if err := backend.Put(t.Context(), route); err == nil {
		t.Fatal("overwrote invalid routing state")
	}
	f.mu.Lock()
	f.data = make([]byte, api.HostingDurableRouteMaxBytes+1)
	f.mu.Unlock()
	if err := backend.Put(t.Context(), route); err == nil {
		t.Fatal("accepted oversized routing state")
	}
}
