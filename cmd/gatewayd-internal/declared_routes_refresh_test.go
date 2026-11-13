package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261028110044 amendment 6: an expired document keeps serving while one
// background read renews it, and a failed renewal keeps the last document
// until staleLimit.
func TestDeclaredRoutesServeLastDocumentWhileRefreshing(t *testing.T) {
	store := &declaredRouteDocStoreStub{doc: []byte(`{"openapi":"3.0.0","paths":{"/old":{"get":{}}}}`)}
	now := time.Unix(1000, 0)
	m := newDeclaredRoutesMatcher(store)
	m.now = func() time.Time { return now }
	m.ttl, m.retry, m.staleLimit = time.Minute, 10*time.Second, 10*time.Minute
	app := gateway.App{ID: "app", AccountID: "acct", OnlyAllowDeclaredRoutes: true}
	match := func(path string) bool {
		t.Helper()
		allowed, err := m.MatchDeclaredRoute(t.Context(), app, path, "GET")
		if err != nil {
			t.Fatalf("match %s: %v", path, err)
		}
		return allowed
	}
	if !match("/old") {
		t.Fatal("first load missed the document")
	}

	store.mu.Lock()
	store.doc, store.err = []byte(`{"openapi":"3.0.0","paths":{"/new":{"get":{}}}}`), errors.New("database stalled")
	store.mu.Unlock()
	now = now.Add(2 * time.Minute)
	if !match("/old") {
		t.Fatal("expired document stopped serving during a failed renewal")
	}
	m.refreshes.Wait()
	if !match("/old") || store.reads != 2 {
		t.Fatalf("failed renewal dropped the document or read on the request path: reads=%d", store.reads)
	}
	now = now.Add(5 * time.Second)
	match("/old")
	m.refreshes.Wait()
	if store.reads != 2 {
		t.Fatalf("failed renewal retried before its retry delay: reads=%d", store.reads)
	}

	store.mu.Lock()
	store.err = nil
	store.mu.Unlock()
	now = now.Add(10 * time.Second)
	match("/old")
	m.refreshes.Wait()
	if !match("/new") || match("/old") {
		t.Fatal("renewal did not publish the new document")
	}

	store.mu.Lock()
	store.err = errors.New("database down")
	store.mu.Unlock()
	now = now.Add(time.Minute + 10*time.Minute)
	if _, err := m.MatchDeclaredRoute(t.Context(), app, "/new", "GET"); err == nil {
		t.Fatal("a document unverified past staleLimit kept admitting requests")
	}
}

type blockingDocStore struct {
	release chan struct{}
	reads   atomic.Int32
}

func (s *blockingDocStore) GetAppOpenAPIDoc(ctx context.Context, _, _ string) ([]byte, state.AppOpenAPIDocMeta, error) {
	s.reads.Add(1)
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, state.AppOpenAPIDocMeta{}, ctx.Err()
	}
	return []byte(`{"openapi":"3.0.0","paths":{"/a":{"get":{}}}}`), state.AppOpenAPIDocMeta{}, nil
}

func TestDeclaredRoutesFirstLoadIsSharedAndBounded(t *testing.T) {
	store := &blockingDocStore{release: make(chan struct{})}
	m := newDeclaredRoutesMatcher(store)
	app := gateway.App{ID: "app", AccountID: "acct", OnlyAllowDeclaredRoutes: true}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if allowed, err := m.MatchDeclaredRoute(context.Background(), app, "/a", "GET"); err != nil || !allowed {
				t.Errorf("shared load: %v/%v", allowed, err)
			}
		}()
	}
	for store.reads.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(store.release)
	wg.Wait()
	if got := store.reads.Load(); got != 1 {
		t.Fatalf("concurrent first loads read %d times, want 1", got)
	}

	stalled := &blockingDocStore{release: make(chan struct{})}
	bounded := newDeclaredRoutesMatcher(stalled)
	bounded.loadTimeout = 20 * time.Millisecond
	if _, err := bounded.MatchDeclaredRoute(context.Background(), app, "/a", "GET"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled first load was not bounded: %v", err)
	}
}

type removalBaselineDocStore struct {
	declaredRouteDocStoreStub
	baseline []byte
	baseErr  error
}

func (s *removalBaselineDocStore) RouteRemovalBaselineOpenAPIDoc(context.Context, string, string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baseline, s.baseErr
}

// A route the current document dropped keeps its removal baseline template as
// its telemetry label, but the baseline never admits a request.
func TestObservedRouteKeepsRemovalBaselineTemplate(t *testing.T) {
	store := &removalBaselineDocStore{
		declaredRouteDocStoreStub: declaredRouteDocStoreStub{doc: []byte(`{"openapi":"3.0.0","paths":{"/users/me":{"get":{}}}}`)},
		baseline:                  []byte(`{"openapi":"3.0.0","paths":{"/users/me":{"get":{}},"/users/{userId}":{"get":{},"delete":{}}}}`),
	}
	m := newDeclaredRoutesMatcher(store)
	app := gateway.App{ID: "app", AccountID: "acct", OnlyAllowDeclaredRoutes: true}
	// The baseline loads in the background; the first request labels without it.
	if _, matched, err := m.ResolveObservedRoute(t.Context(), app, "/users/alice", "GET"); err != nil || matched {
		t.Fatalf("baseline read on the request path: %v/%v", matched, err)
	}
	m.refreshes.Wait()
	for _, tc := range []struct{ path, method, want string }{
		{"/users/alice", "GET", "/users/{userId}"},
		{"/users/alice/", "HEAD", "/users/{userId}"},
		{"/users/alice", "DELETE", "/users/{userId}"},
		{"/users/me", "GET", "/users/me"},
	} {
		template, matched, err := m.ResolveObservedRoute(t.Context(), app, tc.path, tc.method)
		if err != nil || !matched || template != tc.want {
			t.Fatalf("%s %s labeled %q/%v/%v, want %q", tc.method, tc.path, template, matched, err, tc.want)
		}
	}
	if allowed, err := m.MatchDeclaredRoute(t.Context(), app, "/users/alice", "GET"); err != nil || allowed {
		t.Fatalf("removal baseline admitted an undeclared route: %v/%v", allowed, err)
	}

	m.Invalidate(app.ID)
	store.mu.Lock()
	store.baseline, store.baseErr = nil, state.ErrNotFound
	store.mu.Unlock()
	m.ResolveObservedRoute(t.Context(), app, "/users/alice", "GET")
	m.refreshes.Wait()
	if _, matched, err := m.ResolveObservedRoute(t.Context(), app, "/users/alice", "GET"); err != nil || matched {
		t.Fatalf("removed baseline kept labeling: %v/%v", matched, err)
	}
}
