package builderd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Issue #197 B3.11: the cache key is partitioned by plan so a Hobby
// customer's cached layer (built against the Hobby cap) does not serve
// a Pro deploy (which would expect Pro layering / resource sizes).
// These tests pin the load-bearing property of the per-plan partition.

func TestCacheKey_DistinctPerPlan(t *testing.T) {
	// The same (sourceHash, fw) under two different plans produces
	// two distinct cache entries — the load-bearing property of B3.11.
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	const hash = "distinct-plan-hash"
	if err := c.Store(hash, FrameworkNode, api.PlanHobby, src, 7); err != nil {
		t.Fatalf("Store hobby: %v", err)
	}
	if _, ok := c.Lookup(hash, FrameworkNode, api.PlanPro); ok {
		t.Fatal("Lookup returned hit for Pro plan on a Hobby-only cache")
	}
	if _, ok := c.Lookup(hash, FrameworkNode, api.PlanHobby); !ok {
		t.Fatal("Lookup returned miss for Hobby plan on a Hobby-keyed cache")
	}
}

func TestCacheKey_SamePlanHits(t *testing.T) {
	// The same (sourceHash, fw, plan) under two Stores hits the
	// second time — the cache key is correctly normalized by plan.
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	const hash = "same-plan-hash"
	if err := c.Store(hash, FrameworkNode, api.PlanPro, src, 7); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, ok := c.Lookup(hash, FrameworkNode, api.PlanPro)
	if !ok {
		t.Fatal("Lookup expected hit on the same plan")
	}
	if got.Bytes != 7 {
		t.Errorf("bytes = %d, want 7", got.Bytes)
	}
}

func TestCacheKey_RuntimeBasePartition(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("base-specific layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseA := "ghcr.io/poyrazk/runner-node22@sha256:" + strings.Repeat("a", 64)
	baseB := "ghcr.io/poyrazk/runner-node22@sha256:" + strings.Repeat("b", 64)
	recipe := testBuildCacheRecipe("source-hash", FrameworkNode, api.PlanHobby, baseA)
	if err := c.StoreBuild(recipe, src, 19); err != nil {
		t.Fatal(err)
	}
	recipe.RuntimeBaseRef = baseB
	if _, ok := c.LookupBuild(recipe); ok {
		t.Fatal("runtime base change must not reuse the previous cache entry")
	}
	recipe.RuntimeBaseRef = baseA
	if got, ok := c.LookupBuild(recipe); !ok || got.Bytes != 19 {
		t.Fatalf("base-specific cache lookup = %+v, %v; want the baseA entry", got, ok)
	}
}

func TestCacheSweep_PlanPartitionsCleanup(t *testing.T) {
	// Two plans produce two cache directories; the sweep walks both
	// partition directories and evicts based on age regardless of plan.
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("hobby-hash", FrameworkNode, api.PlanHobby, src, 7); err != nil {
		t.Fatalf("Store hobby: %v", err)
	}
	if err := c.Store("pro-hash", FrameworkNode, api.PlanPro, src, 7); err != nil {
		t.Fatalf("Store pro: %v", err)
	}
	// Both partitions exist on disk.
	for _, want := range []string{
		"hobby-hash.node.hobby",
		"pro-hash.node.pro",
	} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Fatalf("expected partition dir %s: %v", want, err)
		}
	}
}
