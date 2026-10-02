package storage

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestUncachedMutableRegionObjects(t *testing.T) {
	ctx := t.Context()
	legacy, _ := NewLocalStorageBackend(t.TempDir())
	eu, _ := NewLocalStorageBackend(t.TempDir())
	router, _ := NewRegionRouter(map[string]StorageBackend{"eu-fsn": eu}, legacy)
	inner, err := NewLocalCacheBackend(router.backend, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := NewLocalCacheBackend(inner, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	router.backend = outer
	key := "regions/eu-fsn/faas-logs/instance/2026/10/2026-10-02.jsonl.gz"
	if err := router.Put(ctx, key, strings.NewReader("cached history")); err != nil {
		t.Fatal(err)
	}
	if err := PutUncached(ctx, router, key, strings.NewReader("fresh history")); err != nil {
		t.Fatal(err)
	}
	body, err := GetUncached(ctx, router, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(raw) != "fresh history" {
		t.Fatalf("mutable read=%q %v", raw, err)
	}
	cached, err := router.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	old, err := io.ReadAll(cached)
	_ = cached.Close()
	if err != nil || string(old) != "cached history" {
		t.Fatalf("cache bypass did not bypass: %q %v", old, err)
	}
	removed, _ := NewRegionRouter(nil, legacy)
	removed.backend = outer
	if _, err := GetUncached(ctx, removed, key); !errors.Is(err, ErrRegionNotConfigured) {
		t.Fatalf("removed route bypassed: %v", err)
	}
	if err := PutUncached(ctx, removed, key, strings.NewReader("wrong")); !errors.Is(err, ErrRegionNotConfigured) {
		t.Fatalf("removed write bypassed: %v", err)
	}
	if err := eu.Delete(ctx, "faas-logs/instance/2026/10/2026-10-02.jsonl.gz"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetUncached(ctx, router, key); !IsNotFound(err) {
		t.Fatalf("missing object served cached bytes: %v", err)
	}
}
