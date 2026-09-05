package builderd

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

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
