package imaged

// adr: 20261026181418
//
// Generation-addressed runtime bases. Each runtime base vmmd admits by
// generation is published in shared storage under its generation's own keys
// (storagekey.BaseGeneration). A node still boots it, and caches it, under
// its logical key. imaged and vmmd route the logical group to the generation
// their release configures, so a release never reads or overwrites another
// release's generation. production-us, 2026-10-26: fsn-3's rc.294 imaged
// restaged base-minimal under the one shared logical key after fsn-2's rc.296
// pre-stage had published rc.296's generation there, and fsn-2's vmmd refused
// every cold boot.

import (
	"context"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

// ConfiguredBaseGenerations maps the key of every runtime base vmmd admits by
// generation (GatedRuntimeBases) to the generation this release configures:
// the image ref imaged stages it from and vmmd boots.
func ConfiguredBaseGenerations(arch string, envLookup func(string) string) (map[string]string, error) {
	gated := GatedRuntimeBases()
	out := make(map[string]string, len(gated))
	for _, runtime := range gated {
		ref, err := resolveDeployBaseRef(runtime, envLookup)
		if err != nil {
			return nil, fmt.Errorf("resolve runtime base generation %q: %w", runtime, err)
		}
		out[storagekey.BaseForArch(runtime, arch)] = ref
	}
	return out, nil
}

// RouteBaseGenerations routes the shared keys of every gated base on this
// handler's storage to the generation this release configures, resolved as
// EnsureBases resolves it, and returns the base keys it routed. A base whose
// ref does not resolve stays unrouted: staging it fails on the same error, so
// imaged never writes its shared keys.
func (h *Handler) RouteBaseGenerations(arch string, envLookup func(string) string) ([]string, error) {
	be, err := h.storageFor()
	if err != nil {
		return nil, fmt.Errorf("imaged: route runtime bases: storage: %w", err)
	}
	generations := make(map[string]string)
	for _, runtime := range GatedRuntimeBases() {
		ref, err := resolveDeployBaseRef(runtime, envLookup)
		if err != nil {
			h.log.Warn("imaged: runtime base not routed to a generation key: its ref does not resolve",
				"runtime", releaseBaseName(runtime), "err", err)
			continue
		}
		generations[storagekey.BaseForArch(runtime, arch)] = ref
	}
	return RouteBaseGenerations(be, generations)
}

// baseGroupKeys lists the artifacts of one runtime base under baseKey's
// naming: the ext4 and its digest, content and scan sidecars.
func baseGroupKeys(baseKey string) []string {
	return []string{baseKey, baseKey + ".digest", baseContentKey(baseKey), wire.ScanKeyForBaseKey(baseKey)}
}

// RouteBaseGenerations routes the group of every configured base to its
// generation's keys in shared storage, and returns the base keys it routed.
// It routes nothing on uncached or node-local storage, which no other node
// shares.
func RouteBaseGenerations(be storage.StorageBackend, generations map[string]string) ([]string, error) {
	keys := make([]string, 0, len(generations))
	for key := range generations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var routed []string
	for _, baseKey := range keys {
		generation := generations[baseKey]
		if generation == "" {
			continue
		}
		logical := baseGroupKeys(baseKey)
		published := baseGroupKeys(storagekey.BaseGeneration(baseKey, generation))
		// vmmd adopts a base, its digest and its scan as one group, so a
		// group is routed whole or not at all.
		shared, err := sharedBaseGroup(be, logical, published)
		if err != nil {
			return nil, fmt.Errorf("imaged: route runtime base %q: %w", baseKey, err)
		}
		if !shared {
			continue
		}
		for i := range logical {
			ok, err := storage.RouteGeneration(be, logical[i], published[i])
			if err == nil && !ok {
				err = fmt.Errorf("%q is node-local", logical[i])
			}
			if err != nil {
				return nil, fmt.Errorf("imaged: route runtime base %q: %w", baseKey, err)
			}
		}
		routed = append(routed, baseKey)
	}
	return routed, nil
}

// sharedBaseGroup reports whether one read-through cache owns every key of a
// base group under both names, with none of them kept on node-local storage.
func sharedBaseGroup(be storage.StorageBackend, logical, published []string) (bool, error) {
	var owner *storage.LocalCacheBackend
	for _, key := range append(append([]string(nil), logical...), published...) {
		cache, rel, err := storage.CacheBackendForKey(be, key)
		if err != nil {
			return false, err
		}
		if cache == nil || (owner != nil && cache != owner) || cache.RoutesLocally(rel) {
			return false, nil
		}
		owner = cache
	}
	return true, nil
}

// checkBaseGenerationRoute refuses to stage ref under a base key routed to
// another generation: the bytes would be published as a generation they are
// not, and every node configured for it would boot them.
func checkBaseGenerationRoute(be storage.StorageBackend, baseKey, ref string) error {
	cache, rel, err := storage.CacheBackendForKey(be, baseKey)
	if err != nil || cache == nil {
		return err
	}
	routed, ok := cache.GenerationRoute(rel)
	if !ok {
		return nil
	}
	_, want, err := storage.CacheBackendForKey(be, storagekey.BaseGeneration(baseKey, ref))
	if err != nil {
		return err
	}
	if routed != want {
		return fmt.Errorf("imaged: base %q is routed to %q, not to the generation of %s", baseKey, routed, ref)
	}
	return nil
}

// publishReusedBase uploads a reused base verified from this node's cache
// when its generation key does not hold it yet: a release before
// ADR-20261026181418 staged it under the shared logical key only. It runs
// before the digest and scan sidecars, so they never commit a generation
// whose base is missing, and records the content sidecar convergence reads.
func (h *Handler) publishReusedBase(ctx context.Context, be storage.StorageBackend, baseKey string) error {
	cache, rel, err := storage.CacheBackendForKey(be, baseKey)
	if err != nil || cache == nil {
		return err
	}
	if _, ok := cache.GenerationRoute(rel); !ok {
		return nil
	}
	uploaded, err := cache.PublishCached(ctx, rel)
	if err != nil {
		return fmt.Errorf("imaged: publish reused base %q under its generation: %w", baseKey, err)
	}
	if !uploaded {
		return nil
	}
	h.log.Info("imaged: published reused runtime base under its generation key", "key", baseKey)
	return h.writeBaseContentSidecar(ctx, be, baseKey)
}
