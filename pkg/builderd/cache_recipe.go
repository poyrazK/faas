package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

// buildCacheRecipeVersion separates these entries from the old archive-only
// identity. Bump it whenever the meaning or encoding of recipe inputs changes.
const buildCacheRecipeVersion = 2

// BuildCacheRecipe identifies a selected application within a source archive.
// The archive digest still covers the full context, including sibling packages.
// The build environment partitions toolchain and platform changes separately
// from the runtime base consumed by the produced application layer.
type BuildCacheRecipe struct {
	SourceSHA256        string    `json:"source_sha256"`
	SourceRoot          string    `json:"source_root"`
	Framework           Framework `json:"framework"`
	Plan                api.Plan  `json:"plan"`
	RuntimeBaseRef      string    `json:"runtime_base_ref"`
	BuilderBaseIdentity string    `json:"builder_base_identity"`
	TargetPlatform      string    `json:"target_platform"`
}

func (r BuildCacheRecipe) key() (string, error) {
	r.BuilderBaseIdentity = strings.TrimSpace(r.BuilderBaseIdentity)
	r.TargetPlatform = strings.TrimSpace(r.TargetPlatform)
	if r.BuilderBaseIdentity == "" {
		return "", errors.New("cache: builder base identity is empty")
	}
	if r.TargetPlatform == "" {
		return "", errors.New("cache: target platform is empty")
	}
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
