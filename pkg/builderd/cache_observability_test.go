package builderd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildCacheRecipeKeySHA256(t *testing.T) {
	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	key, err := recipe.KeySHA256()
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 64 || strings.Trim(key, "0123456789abcdef") != "" {
		t.Fatalf("key digest = %q, want lowercase sha256", key)
	}
}

func TestCacheClassifyBuildInvalidated(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	layer := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(layer, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.StoreBuild(recipe, layer, 8); err != nil {
		t.Fatal(err)
	}
	if got := c.ClassifyBuild(recipe); got != cacheOutcomeHit {
		t.Fatalf("valid entry classified as %q", got)
	}
	if err := os.Remove(filepath.Join(root, mustRecipeKey(t, recipe)+".node.pro", "artifact.sha256")); err != nil {
		t.Fatal(err)
	}
	if got := c.ClassifyBuild(recipe); got != cacheOutcomeInvalidated {
		t.Fatalf("corrupt entry classified as %q, want invalidated", got)
	}
}

func mustRecipeKey(t *testing.T, recipe BuildCacheRecipe) string {
	t.Helper()
	key, err := recipe.key()
	if err != nil {
		t.Fatal(err)
	}
	return key
}
