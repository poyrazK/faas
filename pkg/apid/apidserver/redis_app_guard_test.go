package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt 2026-11-28: generic app routes changed managed caches and broke
// them (a Valkey image deploy replaced one with no maxmemory, a fresh
// restart emptied one, a rename stranded bound apps' REDIS_URL). Only the
// /v1/redis routes change a cache now; reads still work.
func TestManagedCacheRefusesAppRoutes(t *testing.T) {
	e := apidserver.SetupSecrets(t, api.PlanPro)
	store := *e.Store()
	for _, name := range []string{"sessions", "other"} {
		if rec := e.Do(t, "POST", "/v1/redis", api.CreateRedisCacheRequest{Name: name, MemoryMB: 64}, nil); rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	app, err := store.AppBySlug(t.Context(), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if app.Status != state.AppActive {
		if _, err := store.CompareAndSetAppStatus(t.Context(), app.ID, app.Status, state.AppActive); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		method, path string
		body         any
		hint         string
	}{
		{"POST", "/v1/apps/sessions/deployments", map[string]any{"image": "docker.io/valkey/valkey@sha256:" + strings.Repeat("a", 64)}, "gregale redis update sessions"},
		{"POST", "/v1/apps/sessions/restart?fresh=true", nil, "gregale redis restart sessions"},
		{"POST", "/v1/apps/sessions/rename", map[string]any{"slug": "renamed"}, "gregale redis"},
		{"PATCH", "/v1/apps/sessions", map[string]any{"min_instances": 1}, "gregale redis"},
		{"PUT", "/v1/apps/sessions/env/FOO", map[string]any{"value": "1"}, "gregale redis"},
		{"DELETE", "/v1/apps/sessions", nil, "gregale redis delete sessions"},
	} {
		rec := e.Do(t, tc.method, tc.path, tc.body, map[string]string{"Idempotency-Key": tc.method + tc.path})
		var problem api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict ||
			problem.Code != "managed_cache_app" || !strings.Contains(problem.Hint, tc.hint) {
			t.Errorf("%s %s = %d %s; want 409 managed_cache_app naming %q", tc.method, tc.path, rec.Code, rec.Body.String(), tc.hint)
		}
	}
	if rec := e.Do(t, "GET", "/v1/apps/sessions", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("read of a cache app = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if after, err := store.AppBySlug(t.Context(), "sessions"); err != nil || after.MinInstances != 0 || after.Status != state.AppActive {
		t.Fatalf("cache app after refused changes = %+v, %v", after, err)
	}

	// The cache's own routes still change it.
	if rec := e.Do(t, "POST", "/v1/redis/sessions/restart", nil, map[string]string{"Idempotency-Key": "redis-restart"}); rec.Code != http.StatusAccepted {
		t.Fatalf("redis restart = %d %s, want 202", rec.Code, rec.Body.String())
	}
	// A cache never takes a binding: it does not read env.
	if rec := e.Do(t, "POST", "/v1/redis/sessions/bindings", api.BindRedisCacheRequest{App: "other"}, nil); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "cannot be bound to another cache") {
		t.Fatalf("bind into a cache = %d %s, want 400", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, "DELETE", "/v1/redis/sessions", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("redis delete = %d %s, want 204", rec.Code, rec.Body.String())
	}
}
