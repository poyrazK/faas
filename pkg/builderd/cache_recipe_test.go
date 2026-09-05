package builderd

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildRecipeLeaseSurvivesCanonicalSweep(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	artifact := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(artifact, []byte("leased artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	if err := c.StoreBuild(recipe, artifact, 15); err != nil {
		t.Fatal(err)
	}
	entry, ok, err := c.LeaseBuild(recipe, "deployment-1")
	if err != nil || !ok {
		t.Fatalf("LeaseBuild ok=%v err=%v", ok, err)
	}
	if n, err := c.Sweep(1, 100*365*24*time.Hour, time.Now(), nil); err != nil || n != 1 {
		t.Fatalf("Sweep count=%d err=%v", n, err)
	}
	got, err := os.ReadFile(entry.Path)
	if err != nil {
		t.Fatalf("leased artifact was removed with canonical entry: %v", err)
	}
	if string(got) != "leased artifact" {
		t.Fatalf("leased artifact = %q", got)
	}
}

func TestBuildRecipeLeaseRefreshesRecency(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	artifact := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(artifact, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	if err := c.StoreBuild(recipe, artifact, 8); err != nil {
		t.Fatal(err)
	}
	key, _ := recipe.key()
	dir := filepath.Dir(c.entryPath(key, recipe.Framework, recipe.Plan))
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := c.LeaseBuild(recipe, "deployment-2"); err != nil || !ok {
		t.Fatalf("LeaseBuild ok=%v err=%v", ok, err)
	}
	if n, err := c.Sweep(1<<20, 24*time.Hour, time.Now(), nil); err != nil || n != 0 {
		t.Fatalf("recently-used entry swept: count=%d err=%v", n, err)
	}
}

func TestCacheReapLeasesPreservesReferencedAndRemovesOrphan(t *testing.T) {
	root := t.TempDir()
	refs := map[string]bool{"deployment-live": true}
	c := NewCache(root).WithLeaseReferenceChecker(func(_ context.Context, deploymentID, _ string) (bool, error) {
		return refs[deploymentID], nil
	})
	artifact := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(artifact, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	if err := c.StoreBuild(recipe, artifact, 8); err != nil {
		t.Fatal(err)
	}
	live, ok, err := c.LeaseBuild(recipe, "deployment-live")
	if err != nil || !ok {
		t.Fatalf("live lease ok=%v err=%v", ok, err)
	}
	orphan, ok, err := c.LeaseBuild(recipe, "deployment-orphan")
	if err != nil || !ok {
		t.Fatalf("orphan lease ok=%v err=%v", ok, err)
	}
	temp := filepath.Join(root, ".leases", ".lease-abandoned")
	if err := os.WriteFile(temp, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.reapLeases(context.Background(), nil)
	if _, err := os.Stat(live.Path); err != nil {
		t.Fatalf("referenced lease removed: %v", err)
	}
	if _, err := os.Stat(orphan.Path); !os.IsNotExist(err) {
		t.Fatalf("orphan lease remains: %v", err)
	}
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Fatalf("abandoned lease temp remains: %v", err)
	}
}

func TestBuildRecipeCachePartitionsInputs(t *testing.T) {
	c := NewCache(t.TempDir())
	path := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(path, []byte("artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	recipe := BuildCacheRecipe{
		SourceSHA256: "source-a", SourceRoot: "apps/api", Framework: FrameworkNode,
		Plan: api.PlanPro, RuntimeBaseRef: "base-a", BuilderBaseIdentity: "builder-a", TargetPlatform: "linux/amd64",
	}
	if err := c.StoreBuild(recipe, path, 8); err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*BuildCacheRecipe){
		"workspace": func(r *BuildCacheRecipe) { r.SourceRoot = "apps/web" },
		"source":    func(r *BuildCacheRecipe) { r.SourceSHA256 = "source-b" },
		"base":      func(r *BuildCacheRecipe) { r.RuntimeBaseRef = "base-b" },
		"framework": func(r *BuildCacheRecipe) { r.Framework = FrameworkPython },
		"plan":      func(r *BuildCacheRecipe) { r.Plan = api.PlanHobby },
		"builder":   func(r *BuildCacheRecipe) { r.BuilderBaseIdentity = "builder-b" },
		"platform":  func(r *BuildCacheRecipe) { r.TargetPlatform = "linux/arm64" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			other := recipe
			change(&other)
			if _, ok := c.LookupBuild(other); ok {
				t.Fatal("different build inputs reused artifact")
			}
		})
	}
	if _, ok := c.LookupBuild(recipe); !ok {
		t.Fatal("same recipe must hit")
	}
	// A recipe namespace must still participate in normal cache collection.
	n, err := c.Sweep(1<<20, time.Hour, time.Now().Add(2*time.Hour), nil)
	if err != nil || n != 1 {
		t.Fatalf("recipe GC swept=%d err=%v", n, err)
	}
	if _, ok := c.LookupBuild(recipe); ok {
		t.Fatal("GC left expired recipe entry")
	}
}

func TestBuildRecipeRootNormalization(t *testing.T) {
	c := NewCache(t.TempDir())
	path := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(path, []byte("artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	if err := c.StoreBuild(recipe, path, 8); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"", ".", " . "} {
		recipe.SourceRoot = root
		if _, ok := c.LookupBuild(recipe); !ok {
			t.Fatalf("equivalent root %q missed", root)
		}
	}
	recipe.SourceRoot = "apps/api"
	if err := c.StoreBuild(recipe, path, 8); err != nil {
		t.Fatal(err)
	}
	recipe.SourceRoot = " apps/api "
	if _, ok := c.LookupBuild(recipe); !ok {
		t.Fatal("source-context whitespace normalization differs from cache")
	}
	for _, root := range []string{"../api", "/api", "apps/./api", "apps//api", `apps\api`, "apps/\x00api"} {
		recipe.SourceRoot = root
		if _, ok := c.LookupBuild(recipe); ok {
			t.Fatalf("invalid root %q hit", root)
		}
		if err := c.StoreBuild(recipe, path, 8); err == nil {
			t.Fatalf("invalid root %q accepted", root)
		}
	}
}

func TestBuildRecipeRejectsIncompleteEnvironment(t *testing.T) {
	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	for name, change := range map[string]func(*BuildCacheRecipe){
		"empty builder identity": func(r *BuildCacheRecipe) { r.BuilderBaseIdentity = "" },
		"blank builder identity": func(r *BuildCacheRecipe) { r.BuilderBaseIdentity = " " },
		"empty target platform":  func(r *BuildCacheRecipe) { r.TargetPlatform = "" },
		"blank target platform":  func(r *BuildCacheRecipe) { r.TargetPlatform = "\t" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := recipe
			change(&invalid)
			if _, err := invalid.key(); err == nil {
				t.Fatal("incomplete cache recipe was accepted")
			}
		})
	}
}

func TestBuildRecipeRejectsLegacyEntries(t *testing.T) {
	for _, base := range []string{"", "base-ref"} {
		t.Run(base, func(t *testing.T) {
			c := NewCache(t.TempDir())
			path := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(path, []byte("legacy artifact"), 0600); err != nil {
				t.Fatal(err)
			}
			// Reproduce the deployed pre-recipe disk key, including the runtime-base
			// variant. Its output digest is valid, so this must miss due to identity.
			legacyKey := "source"
			if base != "" {
				legacyKey += fmt.Sprintf(".base-%x", sha256.Sum256([]byte(base)))
			}
			if err := c.Store(legacyKey, FrameworkNode, api.PlanPro, path, 15); err != nil {
				t.Fatal(err)
			}
			if _, ok := c.Lookup(legacyKey, FrameworkNode, api.PlanPro); !ok {
				t.Fatal("invalid legacy fixture")
			}
			recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, base)
			for _, root := range []string{"", ".", "apps/api"} {
				recipe.SourceRoot = root
				if _, ok := c.LookupBuild(recipe); ok {
					t.Fatalf("root %q reused unscoped legacy entry", root)
				}
			}
			if err := c.StoreBuild(recipe, path, 15); err != nil {
				t.Fatal(err)
			}
			if _, ok := c.LookupBuild(recipe); !ok {
				t.Fatal("fresh recipe entry must hit")
			}
		})
	}
}
