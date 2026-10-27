package imaged

// adr: 20261024191249, 20261026181418
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
	"strings"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Adoption verdicts for one runtime base on one node.
const (
	// BaseAdoptionCached: this node's cache commits the configured
	// generation, so vmmd boots it without reading the shared copy.
	BaseAdoptionCached = "cached"
	// BaseAdoptionPublished: the scan sidecar under the generation's own
	// keys (ADR-20261026181418), imaged's commit marker, names the
	// configured generation. vmmd adopts it at its first boot of the base
	// here, and no other release writes those keys.
	BaseAdoptionPublished = "published"
	// BaseAdoptionLegacy: the generation has no keys of its own yet, but the
	// shared logical key, written by releases before ADR-20261026181418,
	// names it. vmmd adopts it from there, but a node on such a release can
	// still restage that key with another generation, so the rollout's
	// pre-stage publishes it under its own keys.
	BaseAdoptionLegacy = "legacy"
	// BaseAdoptionUnpublished: no generation of the base was ever
	// published, under its own keys or the shared logical key. No deployment
	// has staged the base, and the first one that does publishes it, as
	// before.
	BaseAdoptionUnpublished = "unpublished"
	// BaseAdoptionStale: neither the generation's keys nor the logical key
	// hold the configured generation with its commit marker, and this node's
	// cache does not hold it. vmmd refuses every boot of the base here with
	// 503 scan_critical.
	BaseAdoptionStale = "stale"
)

// BaseAdoption is one runtime base's adoption state on this node.
type BaseAdoption struct {
	Key        string `json:"key"`
	Generation string `json:"generation"`
	// Cached is the generation this node's cache commits, if any.
	Cached string `json:"cached,omitempty"`
	// Published is the generation the shared scan sidecar names, if any:
	// the generation's own when it exists, else the legacy logical key's.
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
// publications: the generation's own keys first, then the legacy logical key,
// in the order vmmd's ensureBaseGeneration adopts them. Shared copies are
// read from the canonical store by their exact names and never installed in
// the cache, so a serving vmmd here keeps reading what it last adopted.
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
	// The generation's own keys hold only that generation. A scan there
	// that names nothing is a rebuild of it in flight: vmmd refuses until it
	// completes and does not read the legacy key instead. Storage no other
	// node shares has no generation keys: its logical key is the publication.
	shared := cache != nil && !cache.RoutesLocally(rel)
	if shared {
		published, found, err := publishedBaseGeneration(ctx, be, storagekey.BaseGeneration(baseKey, generation))
		if err != nil {
			return adoption, err
		}
		if found {
			adoption.Published = published
			adoption.Verdict = BaseAdoptionStale
			if fcvm.ScanNamesConfiguredBase(published, generation) {
				adoption.Verdict = BaseAdoptionPublished
			}
			return adoption, nil
		}
	}
	published, found, err := publishedBaseGeneration(ctx, be, baseKey)
	if err != nil {
		return adoption, err
	}
	if !found && shared {
		// Releases with generation keys never write the logical key, so a
		// base first published by one shows only under another generation.
		if published, found, err = anotherPublishedGeneration(ctx, be, baseKey); err != nil {
			return adoption, err
		}
	}
	adoption.Published = published
	switch {
	case !found:
		adoption.Verdict = BaseAdoptionUnpublished
	case fcvm.ScanNamesConfiguredBase(published, generation) && shared:
		// Only the logical key can name the configured generation here:
		// its own keys were read above.
		adoption.Verdict = BaseAdoptionLegacy
	case fcvm.ScanNamesConfiguredBase(published, generation):
		adoption.Verdict = BaseAdoptionPublished
	default:
		adoption.Verdict = BaseAdoptionStale
	}
	return adoption, nil
}

// publishedBaseGeneration returns the generation the shared scan sidecar of
// baseKey names, read by its exact name. found is false only when neither the
// scan nor the digest sidecar exists; a digest without a readable scan is a
// publication without its commit marker, which vmmd cannot adopt.
func publishedBaseGeneration(ctx context.Context, be storage.StorageBackend, baseKey string) (string, bool, error) {
	scanKey := wire.ScanKeyForBaseKey(baseKey)
	rc, err := storage.GetParent(ctx, be, scanKey)
	if err == nil {
		var scan struct {
			Image string `json:"image"`
		}
		// A torn sidecar is a publication vmmd cannot adopt, not a read
		// failure: it names no generation.
		image := ""
		if json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&scan) == nil {
			image = scan.Image
		}
		_ = rc.Close()
		return image, true, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return "", false, fmt.Errorf("imaged: read shared scan sidecar %q: %w", scanKey, err)
	}
	digestKey := baseKey + ".digest"
	rc, err = storage.GetParent(ctx, be, digestKey)
	if err == nil {
		_ = rc.Close()
		return "", true, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return "", false, fmt.Errorf("imaged: read shared digest sidecar %q: %w", digestKey, err)
	}
	return "", false, nil
}

// anotherPublishedGeneration reports whether shared storage holds any
// generation of baseKey under its own keys, and the generation the first one
// found names. The caller has already found none for the configured
// generation.
func anotherPublishedGeneration(ctx context.Context, be storage.StorageBackend, baseKey string) (string, bool, error) {
	lister, ok := be.(storage.LocalArtifactLister)
	if !ok {
		return "", false, fmt.Errorf("imaged: list generations of %q: storage cannot list", baseKey)
	}
	keys, err := lister.List(ctx, storagekey.BaseGenerationsPrefix)
	if err != nil {
		return "", false, fmt.Errorf("imaged: list generations of %q: %w", baseKey, err)
	}
	name := "/" + strings.TrimPrefix(baseKey, "base/")
	for _, key := range keys {
		if strings.HasSuffix(key, name) {
			published, _, err := publishedBaseGeneration(ctx, be, key)
			return published, true, err
		}
	}
	return "", false, nil
}
