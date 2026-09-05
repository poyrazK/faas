package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

// buildCacheRecipeVersion separates these entries from the old archive-only
// identity. Bump it whenever the meaning or encoding of recipe inputs changes.
const buildCacheRecipeVersion = 1

// BuildCacheRecipe identifies a selected application within a source archive.
// The archive digest still covers the full context, including sibling packages.
// Builder/toolchain digests and target platform are not captured here yet.
type BuildCacheRecipe struct {
	SourceSHA256   string    `json:"source_sha256"`
	SourceRoot     string    `json:"source_root"`
	Framework      Framework `json:"framework"`
	Plan           api.Plan  `json:"plan"`
	RuntimeBaseRef string    `json:"runtime_base_ref"`
}

func (r BuildCacheRecipe) key() (string, error) {
	root, err := sourcecontext.EffectiveRoot(r.SourceRoot)
	if err != nil {
		return "", fmt.Errorf("cache: source root: %w", err)
	}
	r.SourceRoot = root
	// Structured encoding separates fields even when values contain punctuation.
	// A struct keeps the serialized field order stable.
	data, err := json.Marshal(struct {
		Version int `json:"version"`
		BuildCacheRecipe
	}{Version: buildCacheRecipeVersion, BuildCacheRecipe: r})
	if err != nil {
		return "", fmt.Errorf("cache: encode build recipe: %w", err)
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("recipe-v%d-%s", buildCacheRecipeVersion, hex.EncodeToString(sum[:])), nil
}

// LookupBuild misses on invalid input or any legacy entry. Falling back to an
// archive-only key could return an artifact produced for a different member.
func (c *Cache) LookupBuild(recipe BuildCacheRecipe) (CacheEntry, bool) {
	key, err := recipe.key()
	if err != nil {
		return CacheEntry{}, false
	}
	return c.lookupKey(key, recipe.Framework, recipe.Plan)
}

// StoreBuild publishes under the same normalized recipe used by LookupBuild.
func (c *Cache) StoreBuild(recipe BuildCacheRecipe, layerPath string, bytes int64) error {
	key, err := recipe.key()
	if err != nil {
		return err
	}
	return c.storeKey(key, recipe.Framework, recipe.Plan, layerPath, bytes)
}
