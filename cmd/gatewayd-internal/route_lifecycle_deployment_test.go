package main

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type lifecycleCaptureStore struct {
	declaredRouteDocStoreStub
	docs         map[string][]byte
	metas        map[string]state.OpenAPIDocMeta
	captureReads int
	duringRead   func()
}

func (s *lifecycleCaptureStore) GetDeploymentOpenAPIDoc(_ context.Context, id, account string) ([]byte, state.OpenAPIDocMeta, error) {
	s.captureReads++
	doc, ok := s.docs[id]
	if !ok {
		return nil, state.OpenAPIDocMeta{}, state.ErrNotFound
	}
	meta := s.metas[id]
	if s.duringRead != nil {
		s.duringRead()
	}
	return doc, meta, nil
}
func TestDeploymentLifecycleIsolationAndInvalidation(t *testing.T) {
	first := []byte(`{"openapi":"3.0.0","paths":{"/old/{id}":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z"}},"/old/current":{"get":{},"head":{}}}}`)
	second := []byte(`{"openapi":"3.0.0","paths":{"/old/{id}":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-02T00:00:00Z"}}}}`)
	store := &lifecycleCaptureStore{docs: map[string][]byte{"production": first, "canary": second}, metas: map[string]state.OpenAPIDocMeta{}}
	for id, doc := range store.docs {
		hash := sha256.Sum256(doc)
		store.metas[id] = state.OpenAPIDocMeta{AppID: "app", AccountID: "account", DeploymentID: id, Source: "cold_boot", DocSHA256: hash[:]}
	}
	matcher := newDeclaredRoutesMatcher(store)
	app := gateway.App{ID: "app", AccountID: "account", PinnedDeploymentID: "production", PinnedDeploymentScope: "production"}
	for _, tc := range []struct{ id, path, method, date string }{
		{"production", "/old/42", "GET", "2026-10-01T00:00:00Z"}, {"canary", "/old/42", "HEAD", "2026-10-02T00:00:00Z"}, {"production", "/old/current", "HEAD", ""}, {"production", "/old/42", "POST", ""},
	} {
		metadata, err := matcher.ResolveDeploymentRouteLifecycle(context.Background(), app, tc.id, tc.path, tc.method)
		if err != nil {
			t.Fatal(err)
		}
		actual := ""
		if !metadata.DeprecatedAt.IsZero() {
			actual = metadata.DeprecatedAt.Format(time.RFC3339)
		}
		if actual != tc.date {
			t.Fatalf("%+v: %s", tc, actual)
		}
	}
	if store.captureReads != 2 || store.reads != 0 {
		t.Fatalf("cache reads %d app reads %d", store.captureReads, store.reads)
	}
	store.docs["production"] = second
	matcher.Invalidate(app.ID)
	metadata, err := matcher.ResolveDeploymentRouteLifecycle(context.Background(), app, "production", "/old/42", "GET")
	if err != nil || metadata.DeprecatedAt.Format(time.RFC3339) != "2026-10-02T00:00:00Z" {
		t.Fatalf("invalidation %+v %v", metadata, err)
	}
	// Cached entries must not cross account or app boundaries.
	foreign := app
	foreign.AccountID = "other"
	if _, err := matcher.ResolveDeploymentRouteLifecycle(context.Background(), foreign, "production", "/old/42", "GET"); err == nil {
		t.Fatal("account isolation")
	}
	foreign = app
	foreign.ID = "other"
	if _, err := matcher.ResolveDeploymentRouteLifecycle(context.Background(), foreign, "production", "/old/42", "GET"); err == nil {
		t.Fatal("app isolation")
	}
	meta := store.metas["production"]
	meta.Truncated = true
	store.metas["production"] = meta
	matcher.Invalidate(app.ID)
	if _, err := matcher.ResolveDeploymentRouteLifecycle(context.Background(), app, "production", "/old/42", "GET"); err == nil {
		t.Fatal("truncated capture")
	}
	meta.Truncated = false
	store.metas["production"] = meta
	// A notification during an in-flight read must not refill an invalidated cache.
	store.duringRead = func() { matcher.Invalidate(app.ID) }
	if _, err := matcher.ResolveDeploymentRouteLifecycle(context.Background(), app, "production", "/old/42", "GET"); err != nil {
		t.Fatal(err)
	}
	matcher.mu.RLock()
	entries := len(matcher.deploymentEntries)
	matcher.mu.RUnlock()
	if entries != 0 {
		t.Fatal("stale read repopulated cache")
	}
}
