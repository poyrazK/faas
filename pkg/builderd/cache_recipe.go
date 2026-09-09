package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

const (
	cacheOutcomeHit         = "hit"
	cacheOutcomeMiss        = "miss"
	cacheOutcomeInvalidated = "invalidated"
)

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

// KeySHA256 returns the digest portion of the versioned cache key. The public
// cache path keeps its recipe-vN prefix for on-disk compatibility, while API
// consumers receive the stable 64-character digest only.
func (r BuildCacheRecipe) KeySHA256() (string, error) {
	key, err := r.key()
	if err != nil {
		return "", err
	}
	const separator = "-"
	idx := strings.LastIndex(key, separator)
	if idx < 0 || idx == len(key)-1 {
		return "", errors.New("cache: malformed build recipe key")
	}
	return key[idx+len(separator):], nil
}

// LookupBuild misses on invalid input or any legacy entry. Falling back to an
// archive-only key could return an artifact produced for a different member.
func (c *Cache) LookupBuild(recipe BuildCacheRecipe) (CacheEntry, bool) {
	key, err := recipe.key()
	if err != nil {
		return CacheEntry{}, false
	}
	if c == nil {
		return CacheEntry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.lookupKey(key, recipe.Framework, recipe.Plan)
	if ok {
		c.touchEntry(entry.Path)
	}
	return entry, ok
}

// ClassifyBuild reports why a recipe lookup will not be a hit. A present
// artifact that fails the integrity sidecars is an invalidation; an absent
// artifact is an ordinary miss. The classification is advisory and the
// subsequent LeaseBuild call remains the authority for serving the artifact.
func (c *Cache) ClassifyBuild(recipe BuildCacheRecipe) string {
	if c == nil || c.root == "" {
		return cacheOutcomeMiss
	}
	key, err := recipe.key()
	if err != nil {
		return cacheOutcomeMiss
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	path := c.entryPath(key, recipe.Framework, recipe.Plan)
	if _, err := os.Stat(path); err != nil {
		return cacheOutcomeMiss
	}
	if _, ok := c.lookupKey(key, recipe.Framework, recipe.Plan); ok {
		return cacheOutcomeHit
	}
	return cacheOutcomeInvalidated
}

// StoreBuild publishes under the same normalized recipe used by LookupBuild.
func (c *Cache) StoreBuild(recipe BuildCacheRecipe, layerPath string, bytes int64) error {
	return c.storeBuild(recipe, layerPath, bytes, CacheToolchain{})
}

// StoreBuildWithToolchain publishes a cache artifact together with the
// toolchain versions observed by guest-init. The metadata is advisory; the
// artifact integrity sidecars remain the cache-hit gate.
func (c *Cache) StoreBuildWithToolchain(recipe BuildCacheRecipe, layerPath string, bytes int64, toolchain CacheToolchain) error {
	return c.storeBuild(recipe, layerPath, bytes, toolchain)
}

func (c *Cache) storeBuild(recipe BuildCacheRecipe, layerPath string, bytes int64, toolchain CacheToolchain) error {
	key, err := recipe.key()
	if err != nil {
		return err
	}
	if c == nil {
		return errors.New("cache: not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.storeKey(key, recipe.Framework, recipe.Plan, layerPath, bytes, toolchain)
}
