// adr: 20261022184531
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestBaseGenerationAdoptable(t *testing.T) {
	const (
		baseKey  = "base/base-minimal-amd64.ext4"
		rc288Ref = "ghcr.io/p/base-minimal@sha256:db529ed"
		rc290Ref = "ghcr.io/p/base-minimal@sha256:5ca69ea"
		rc291Ref = "ghcr.io/p/base-minimal@sha256:7e10f3c"
	)
	scanKey := wire.ScanKeyForBaseKey(baseKey)
	scan := func(image string) string {
		b, _ := json.Marshal(map[string]any{"image": image, "findings": map[string]int{"CRITICAL": 0}, "fix_available_findings": map[string]int{"CRITICAL": 0}})
		return string(b)
	}
	for _, tc := range []struct {
		name string
		// cached is the generation this node's cache holds ("" for none);
		// canonical is the generation shared storage holds.
		cached, canonical string
		parentErr         error
		configured        string
		wantAdoptable     bool
	}{
		{name: "cache holds the configured generation", cached: rc288Ref, canonical: rc290Ref, configured: rc288Ref, wantAdoptable: true},
		{name: "shared storage holds it, a wake would refresh", canonical: rc288Ref, configured: rc288Ref, wantAdoptable: true},
		// production-us 2026-10-22: fsn-3 rolled back to rc.288 after
		// fsn-2's rc.290 imaged had published 5ca69ea.
		{name: "neither holds it", canonical: rc290Ref, configured: rc288Ref},
		{name: "cache holds another generation", cached: rc290Ref, canonical: rc290Ref, configured: rc288Ref},
		{name: "cache and shared storage hold two other generations", cached: rc290Ref, canonical: rc291Ref, configured: rc288Ref},
		{name: "shared storage unreadable is no evidence", parentErr: errors.New("gcs: 503"), configured: rc288Ref, wantAdoptable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			parent := &memStorage{}
			cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			put := func(key, value string) {
				t.Helper()
				if err := parent.Put(ctx, key, strings.NewReader(value)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.cached != "" {
				put(baseKey, "base-"+tc.cached)
				put(scanKey, scan(tc.cached))
				for _, key := range []string{baseKey, scanKey} {
					rc, err := cache.Get(ctx, key)
					if err != nil {
						t.Fatal(err)
					}
					_ = rc.Close()
				}
				if err := cache.MarkGeneration(baseKey, tc.cached); err != nil {
					t.Fatal(err)
				}
			}
			if tc.canonical != "" {
				put(baseKey, "base-"+tc.canonical)
				put(scanKey, scan(tc.canonical))
			}
			parent.getErr = tc.parentErr
			m := &Manager{}
			m.WithStorage(cache)
			m.WithBaseGenerations(map[string]string{baseKey: tc.configured})

			report := m.checkBaseGenerations(ctx)
			if len(report) != 1 || report[0].BaseKey != baseKey || report[0].Generation != tc.configured || report[0].Adoptable != tc.wantAdoptable ||
				(tc.wantAdoptable != (report[0].Reason == "")) {
				t.Fatalf("report = %+v, want adoptable=%t", report, tc.wantAdoptable)
			}
			// The check reads shared storage without refreshing the cache:
			// a node still serving its cached generation must not be moved
			// onto another one by a readiness probe.
			if tc.cached != "" {
				if generation, ok, err := cache.CachedGeneration(baseKey); err != nil || !ok || generation != tc.cached {
					t.Fatalf("cached generation after the check = %q, %t, %v; want %s untouched", generation, ok, err, tc.cached)
				}
				parent.getErr = errors.New("parent offline")
				rc, err := cache.Get(ctx, scanKey)
				if err != nil {
					t.Fatalf("cached scan sidecar evicted by the check: %v", err)
				}
				b, _ := io.ReadAll(rc)
				_ = rc.Close()
				if !strings.Contains(string(b), tc.cached) {
					t.Fatalf("cached scan sidecar replaced by the check: %s", b)
				}
				parent.getErr = nil
			}
			if tc.cached == tc.configured && tc.cached != "" {
				if err := m.bringUpScanCheck(ctx, baseKey); err != nil {
					t.Fatalf("a wake after the check was refused: %v", err)
				}
			}
		})
	}
}

func TestBaseGenerationReadiness_NeverWaitsForStorage(t *testing.T) {
	const baseKey = "base/base-minimal-amd64.ext4"
	m := &Manager{}
	if got := m.BaseGenerationReadiness(context.Background()); got != nil {
		t.Fatalf("a vmmd without configured generations reported %+v", got)
	}
	parent := &memStorage{}
	cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	m.WithStorage(cache)
	m.WithBaseGenerations(map[string]string{baseKey: "ghcr.io/p/base-minimal@sha256:db529ed"})
	if got := m.BaseGenerationReadiness(context.Background()); len(got) != 0 {
		t.Fatalf("first report = %+v, want none until the background check finishes", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := m.BaseGenerationReadiness(context.Background())
		if len(got) == 1 && got[0].BaseKey == baseKey {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background check never reported; last = %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
