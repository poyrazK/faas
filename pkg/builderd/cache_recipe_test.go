package builderd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		"scope":      func(r *BuildCacheRecipe) { r.GitOpsReuseScope = "another-app-or-recipe" },
		"workspace":  func(r *BuildCacheRecipe) { r.SourceRoot = "apps/web" },
		"dockerfile": func(r *BuildCacheRecipe) { r.DockerfilePath = "deploy/Dockerfile.production" },
		"source":     func(r *BuildCacheRecipe) { r.SourceSHA256 = "source-b" },
		"base":       func(r *BuildCacheRecipe) { r.RuntimeBaseRef = "base-b" },
		"framework":  func(r *BuildCacheRecipe) { r.Framework = FrameworkPython },
		"plan":       func(r *BuildCacheRecipe) { r.Plan = api.PlanHobby },
		"builder":    func(r *BuildCacheRecipe) { r.BuilderBaseIdentity = "builder-b" },
		"platform":   func(r *BuildCacheRecipe) { r.TargetPlatform = "linux/arm64" },
		"function":   func(r *BuildCacheRecipe) { r.Function = !r.Function },
		"secrets":    func(r *BuildCacheRecipe) { r.BuildSecretScope = "account-app-scope" },
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

// A build that mounts secrets used a recipe naming no tenant, so another
// account's byte-identical source with the same Dockerfile options and secret
// names was served an image built with the first account's tokens.
func TestBuildSecretScopePartitionsTenantsAndValues(t *testing.T) {
	secrets := []SealedBuildSecret{{Key: "NPM_TOKEN", Ciphertext: []byte("sealed-a")}, {Key: "GIT_TOKEN", Ciphertext: []byte("sealed-b")}}
	base := buildSecretScope("acct-1", "app-1", "default", secrets)
	if base == "" || buildSecretScope("acct-1", "app-1", "default", nil) != "" {
		t.Fatal("scope must be set exactly when secrets are mounted")
	}
	reversed := []SealedBuildSecret{secrets[1], secrets[0]}
	if buildSecretScope("acct-1", "app-1", "default", reversed) != base {
		t.Fatal("secret order changed the scope")
	}
	rotated := []SealedBuildSecret{{Key: "NPM_TOKEN", Ciphertext: []byte("sealed-c")}, secrets[1]}
	for name, other := range map[string]string{
		"account": buildSecretScope("acct-2", "app-1", "default", secrets),
		"app":     buildSecretScope("acct-1", "app-2", "default", secrets),
		"scope":   buildSecretScope("acct-1", "app-1", "staging", secrets),
		"value":   buildSecretScope("acct-1", "app-1", "default", rotated),
	} {
		if other == base {
			t.Errorf("a different %s shared the secret build scope", name)
		}
	}
	// Builds without secrets keep their existing keys.
	recipe := BuildCacheRecipe{SourceSHA256: "source-a", Framework: FrameworkNode, Plan: api.PlanPro,
		RuntimeBaseRef: "base-a", BuilderBaseIdentity: "builder-a", TargetPlatform: "linux/amd64"}
	plain, err := recipe.KeySHA256()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(recipe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "build_secret_scope") || plain == "" {
		t.Fatalf("recipe without secrets encodes the scope: %s", encoded)
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

func TestBuildRecipeDockerfileNormalization(t *testing.T) {
	recipe := testBuildCacheRecipe("source", FrameworkDocker, api.PlanPro, "base")
	recipe.DockerfilePath = "deploy/Dockerfile.production"
	if _, err := recipe.key(); err != nil {
		t.Fatalf("valid Dockerfile path: %v", err)
	}
	for _, dockerfile := range []string{"../Dockerfile", "/Dockerfile", "deploy/./Dockerfile", `deploy\Dockerfile`} {
		recipe.DockerfilePath = dockerfile
		if _, err := recipe.key(); err == nil {
			t.Fatalf("invalid Dockerfile path %q accepted", dockerfile)
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

func TestBuildRecipeGitOpsOriginReplacementPreservesLeasedBytes(t *testing.T) {
	cache := NewCache(t.TempDir())
	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	recipe.GitOpsReuseScope = "reviewed-app-and-recipe"
	artifact := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(artifact, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreBuildWithToolchain(recipe, artifact, 8, CacheToolchain{OriginDeploymentID: "first", OriginBuildID: "first-build"}); err != nil {
		t.Fatal(err)
	}
	leased, ok, err := cache.LeaseBuild(recipe, "target-lease")
	if err != nil || !ok {
		t.Fatal("lease", err)
	}
	if err := os.WriteFile(artifact, []byte("replacement"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreBuildWithToolchain(recipe, artifact, 11, CacheToolchain{OriginDeploymentID: "second", OriginBuildID: "second-build"}); err != nil {
		t.Fatal(err)
	}
	current, ok := cache.LookupBuild(recipe)
	if !ok || current.Toolchain.OriginDeploymentID != "second" || current.Bytes != 11 {
		t.Fatal("stale origin was retained", current)
	}
	original, err := os.ReadFile(leased.Path)
	if err != nil || string(original) != "original" {
		t.Fatal("replacement modified in-flight leased bytes", err)
	}
	// A reuse that leased the replaced bytes cannot rewrite the new origin.
	if err := cache.UpdateBuildOrigins(recipe, leased.Path, CacheToolchain{OriginDeploymentID: "first", NextOriginDeploymentID: "target"}); err != nil {
		t.Fatal(err)
	}
	if current, ok := cache.LookupBuild(recipe); !ok || current.Toolchain.OriginDeploymentID != "second" || current.Toolchain.NextOriginDeploymentID != "" {
		t.Fatal("stale lease rewrote replacement origin", current)
	}
}

func TestBuildRecipeGitOpsFallbackOriginKeepsBytesAndOrigin(t *testing.T) {
	cache := NewCache(t.TempDir())
	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	recipe.GitOpsReuseScope = "reviewed-app-and-recipe"
	artifact := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(artifact, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreBuildWithToolchain(recipe, artifact, 8, CacheToolchain{OriginDeploymentID: "origin", OriginBuildID: "origin-build", BuildkitVer: "kit"}); err != nil {
		t.Fatal(err)
	}
	before, ok := cache.LookupBuild(recipe)
	if !ok {
		t.Fatal("entry")
	}
	leased, ok, err := cache.LeaseBuild(recipe, "target-lease")
	if err != nil || !ok {
		t.Fatal("lease", err)
	}
	metadata := leased.Toolchain
	metadata.NextOriginDeploymentID, metadata.NextOriginBuildID = "target", "target-build"
	if err := cache.UpdateBuildOrigins(recipe, leased.Path, metadata); err != nil {
		t.Fatal(err)
	}
	after, ok := cache.LookupBuild(recipe)
	if !ok || after.Toolchain.OriginDeploymentID != "origin" || after.Toolchain.NextOriginDeploymentID != "target" || after.Toolchain.NextOriginBuildID != "target-build" || after.Toolchain.BuildkitVer != "kit" {
		t.Fatal("fallback origin not recorded beside the verified origin", after.Toolchain)
	}
	beforeInfo, err := os.Stat(before.Path)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(after.Path)
	if err != nil || !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("metadata update copied the cached bytes", err)
	}
}
