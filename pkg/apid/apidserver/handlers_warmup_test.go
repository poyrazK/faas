package apidserver_test

// adr: 20261114153000 — lifecycle.warmup names the path that primes a
// deployment's first init snapshot.

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestAppWarmupLifecycleRoundTrip(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	hook := &api.WarmupHook{Path: "/internal/warmup"}
	rec := e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "warmup-app", Warmup: hook}, nil)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Manifest.Warmup == nil || *out.Manifest.Warmup != *hook {
		t.Fatalf("create response warmup = %+v", out.Manifest.Warmup)
	}
	updated := &api.WarmupHook{Path: "/work"}
	rec = e.Do(t, "PATCH", "/v1/apps/warmup-app", api.UpdateAppRequest{Warmup: updated}, nil)
	if rec.Code != 200 {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	stored, err := (*e.Store()).AppBySlug(t.Context(), "warmup-app")
	if err != nil || stored.Manifest.Warmup == nil || *stored.Manifest.Warmup != *updated {
		t.Fatalf("stored warmup = %+v, err=%v", stored.Manifest.Warmup, err)
	}
	rec = e.Do(t, "PATCH", "/v1/apps/warmup-app", api.UpdateAppRequest{Warmup: &api.WarmupHook{}}, nil)
	if rec.Code != 200 {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body)
	}
	stored, err = (*e.Store()).AppBySlug(t.Context(), "warmup-app")
	if err != nil || stored.Manifest.Warmup != nil {
		t.Fatalf("cleared warmup = %+v, err=%v", stored.Manifest.Warmup, err)
	}
}

func TestAppWarmupRejectsInvalidPath(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	for _, path := range []string{"warmup", "//evil.example/x", "/x?y=1"} {
		rec := e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "bad-warmup", Warmup: &api.WarmupHook{Path: path}}, nil)
		if rec.Code != 400 && rec.Code != 422 {
			t.Fatalf("create with warmup %q: %d %s, want a validation error", path, rec.Code, rec.Body)
		}
	}
}
