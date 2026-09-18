// adr: 045
// issue: 1278
package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeConfigStoreStub struct {
	rows []state.AppEnv
}

func (s runtimeConfigStoreStub) ListAppEnv(context.Context, string, string) ([]state.AppEnv, error) {
	return s.rows, nil
}

func TestLoadRuntimeConfigReturnsDefaultScopeAndRevision(t *testing.T) {
	updated := time.Date(2026, time.September, 18, 20, 0, 0, 123, time.UTC)
	response, err := loadRuntimeConfig(context.Background(), runtimeConfigStoreStub{rows: []state.AppEnv{
		{Scope: "default", Key: "FEATURE_FLAG", Value: "on", UpdatedAt: updated},
		{Scope: "staging", Key: "FEATURE_FLAG", Value: "off", UpdatedAt: updated.Add(time.Hour)},
	}}, "acct-1", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Env["FEATURE_FLAG"] != "on" {
		t.Fatalf("env = %+v, want default scope value", response.Env)
	}
	if response.Revision != updated.Format(time.RFC3339Nano) {
		t.Fatalf("revision = %q, want %q", response.Revision, updated.Format(time.RFC3339Nano))
	}
}

func TestLoadRuntimeConfigReturnsEmptyMapWithoutRows(t *testing.T) {
	response, err := loadRuntimeConfig(context.Background(), runtimeConfigStoreStub{}, "acct-1", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Env == nil || len(response.Env) != 0 {
		t.Fatalf("env = %#v, want an empty map", response.Env)
	}
	if response.Revision != "" {
		t.Fatalf("revision = %q, want empty", response.Revision)
	}
}

func TestRuntimeConfigCacheExpiresAndCopies(t *testing.T) {
	cache := newRuntimeConfigCache()
	loaded := time.Date(2026, time.September, 18, 20, 0, 0, 0, time.UTC)
	cache.put("acct-1", "app-1", runtimeConfigResponse{Env: map[string]string{"FEATURE_X": "on"}, Revision: "r1"}, loaded)

	got, ok := cache.get("acct-1", "app-1", loaded.Add(time.Second))
	if !ok || got.Env["FEATURE_X"] != "on" {
		t.Fatalf("cache get = (%+v, %t)", got, ok)
	}
	got.Env["FEATURE_X"] = "mutated"
	again, ok := cache.get("acct-1", "app-1", loaded.Add(2*time.Second))
	if !ok || again.Env["FEATURE_X"] != "on" {
		t.Fatalf("cache returned aliased response = (%+v, %t)", again, ok)
	}
	if _, ok := cache.get("acct-1", "app-1", loaded.Add(runtimeConfigCacheTTL)); ok {
		t.Fatal("expired cache entry remained available")
	}
}

func TestRuntimeConfigCacheInvalidatesByIdentity(t *testing.T) {
	cache := newRuntimeConfigCache()
	now := time.Now()
	cache.put("acct-1", "app-1", runtimeConfigResponse{Revision: "r1"}, now)
	cache.put("acct-2", "app-1", runtimeConfigResponse{Revision: "r2"}, now)
	cache.put("acct-1", "app-2", runtimeConfigResponse{Revision: "r3"}, now)

	cache.invalidate("acct-1", "app-1")
	if _, ok := cache.get("acct-1", "app-1", now); ok {
		t.Fatal("account-specific invalidation did not remove entry")
	}
	if _, ok := cache.get("acct-2", "app-1", now); !ok {
		t.Fatal("account-specific invalidation removed another account")
	}
	cache.invalidate("", "app-1")
	if _, ok := cache.get("acct-2", "app-1", now); ok {
		t.Fatal("app-wide invalidation did not remove entry")
	}
	if _, ok := cache.get("acct-1", "app-2", now); !ok {
		t.Fatal("app-wide invalidation removed another app")
	}
}
