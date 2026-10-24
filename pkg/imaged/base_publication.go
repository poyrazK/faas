package imaged

// adr: 20261024191249
//
// A runtime base's adoption state on one node, judged the way vmmd's
// ensureBaseGeneration will judge it. The rollout's pre-stage uses it to
// decide which bases to publish, and `vmmd base-adoption-check` uses it to
// keep a node out of service until it can boot every base its release
// configures. rc.293 on production-us (2026-10-24): fsn-2 rolled first, its
// pre-stage published nothing because it owned no app, the shared scan
// sidecar kept naming the previous base-minimal generation, and the rolled
// vmmd refused every boot with 503 scan_critical once the node was activated.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Adoption verdicts for one runtime base on one node.
const (
	// BaseAdoptionCached: this node's cache commits the configured
	// generation, so vmmd boots it without reading the shared copy.
	BaseAdoptionCached = "cached"
	// BaseAdoptionPublished: the shared scan sidecar, imaged's commit
	// marker, names the configured generation. vmmd adopts it at its first
	// boot of the base here.
	BaseAdoptionPublished = "published"
	// BaseAdoptionUnpublished: neither the scan nor the digest sidecar was
	// ever published. No deployment has staged the base, and the first one
	// that does publishes it, as before.
	BaseAdoptionUnpublished = "unpublished"
	// BaseAdoptionStale: the shared copy is another generation, or lacks
	// its commit marker, and this node's cache does not hold the configured
	// one. vmmd refuses every boot of the base here with 503 scan_critical.
	BaseAdoptionStale = "stale"
)

// BaseAdoption is one runtime base's adoption state on this node.
type BaseAdoption struct {
	Key        string `json:"key"`
	Generation string `json:"generation"`
	// Cached is the generation this node's cache commits, if any.
	Cached string `json:"cached,omitempty"`
	// Published is the generation the shared scan sidecar names, if any.
	Published string `json:"published,omitempty"`
	Verdict   string `json:"verdict"`
}

// Adoptable reports whether vmmd can boot the base's configured generation
// here, or nothing needs it yet.
func (a BaseAdoption) Adoptable() bool { return a.Verdict != BaseAdoptionStale }

// GatedRuntimeBases lists the runtimes whose base vmmd admits by generation:
// the minimal base ("") and every function runtime. The Debian parent is a
// build input of its children and is never booted.
func GatedRuntimeBases() []string {
	out := []string{""}
	for _, row := range DefaultRuntimeBaseRefs {
		if row.Runtime != RuntimeDebianParent {
			out = append(out, row.Runtime)
		}
	}
	return out
}

// JudgeBaseAdoption reads baseKey's cache marker on this node and its shared
// publication. The shared copy is read from the canonical store and never
// installed in the cache, so a serving vmmd here keeps reading what it last
// adopted.
func JudgeBaseAdoption(ctx context.Context, be storage.StorageBackend, baseKey, generation string) (BaseAdoption, error) {
	adoption := BaseAdoption{Key: baseKey, Generation: generation}
	if generation == "" {
		return adoption, fmt.Errorf("imaged: judge base %q: no configured generation", baseKey)
	}
	cache, rel, err := storage.CacheBackendForKey(be, baseKey)
	if err != nil {
		return adoption, fmt.Errorf("imaged: judge base %q: %w", baseKey, err)
	}
	if cache != nil {
		cached, ok, err := cache.CachedGeneration(rel)
		if err != nil {
			return adoption, fmt.Errorf("imaged: judge base %q: read cache generation: %w", baseKey, err)
		}
		if ok {
			adoption.Cached = cached
		}
		if ok && cached == generation {
			adoption.Verdict = BaseAdoptionCached
			return adoption, nil
		}
	}
	published, found, err := publishedBaseGeneration(ctx, be, baseKey)
	if err != nil {
		return adoption, err
	}
	adoption.Published = published
	switch {
	case !found:
		adoption.Verdict = BaseAdoptionUnpublished
	case fcvm.ScanNamesConfiguredBase(published, generation):
		adoption.Verdict = BaseAdoptionPublished
	default:
		adoption.Verdict = BaseAdoptionStale
	}
	return adoption, nil
}

// publishedBaseGeneration returns the generation the shared scan sidecar
// names. found is false only when neither the scan nor the digest sidecar
// exists; a digest without a readable scan is a publication without its
// commit marker, which vmmd cannot adopt.
func publishedBaseGeneration(ctx context.Context, be storage.StorageBackend, baseKey string) (string, bool, error) {
	scanKey := wire.ScanKeyForBaseKey(baseKey)
	rc, err := storage.GetCanonical(ctx, be, scanKey)
	if err == nil {
		var scan struct {
			Image string `json:"image"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&scan)
		_ = rc.Close()
		if decodeErr != nil {
			return "", true, nil
		}
		return scan.Image, true, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return "", false, fmt.Errorf("imaged: read shared scan sidecar %q: %w", scanKey, err)
	}
	digestKey := baseKey + ".digest"
	rc, err = storage.GetCanonical(ctx, be, digestKey)
	if err == nil {
		_ = rc.Close()
		return "", true, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return "", false, fmt.Errorf("imaged: read shared digest sidecar %q: %w", digestKey, err)
	}
	return "", false, nil
}
