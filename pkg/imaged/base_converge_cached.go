package imaged

// Cached-base convergence — ADR-632 (amends ADR-567).
//
// ADR-567 converged only the bases this imaged process staged. Runtime bases
// are staged lazily (only the builder, the minimal base and the node's
// assigned runtimes are staged at startup), so a base cached by an earlier
// daemon stayed whatever bytes that daemon built until a deployment of that
// runtime happened to land on this node again. vmmd keeps attaching that
// cached copy, and ADR-510 then refuses every cross-node restore of a
// snapshot captured on the other node's bytes. On production-us (rc.243)
// python312 and go124 diverged on one compute node and python313 on the
// other; about 137 wakes a day fell back to a cold boot and recaptured.
//
// Every convergence pass therefore also considers each platform runtime base
// that is cached on this node, using the ref this daemon would build it from.
// convergeBase's recipe check is unchanged: the copy is replaced only by a
// publication of that same source ref built with this daemon's guest-init.

import (
	"context"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
)

// cachedBaseRuntimes lists the runtime bases a node can hold without having
// staged them in this process: the minimal base ("") and every runtime in the
// matrix. The builder and execution-profile bases are staged at every start.
func cachedBaseRuntimes() []string {
	out := make([]string, 0, len(DefaultRuntimeBaseRefs)+1)
	out = append(out, "")
	for _, row := range DefaultRuntimeBaseRefs {
		out = append(out, row.Runtime)
	}
	return out
}

// rememberCachedBases registers every platform base cached on this node that
// this daemon has not staged itself, so the pass that follows keeps it
// aligned with its publication.
func (h *Handler) rememberCachedBases(ctx context.Context, be storage.StorageBackend, arch string, envLookup func(string) string) {
	for _, runtime := range cachedBaseRuntimes() {
		if ctx.Err() != nil {
			return
		}
		baseKey := sched.BaseKeyForArch(runtime, arch)
		s := &h.baseConvergence
		s.mu.Lock()
		_, staged := s.staged[baseKey]
		s.mu.Unlock()
		if staged {
			continue
		}
		cache, rel, err := storage.CacheBackendForKey(be, baseKey)
		if err != nil || cache == nil {
			continue
		}
		if _, local, err := cache.LocalPath(rel); err != nil || !local {
			continue
		}
		ref, err := resolveDeployBaseRef(runtime, envLookup)
		if err != nil {
			// The node would refuse to stage this runtime too; leave its
			// cached copy alone rather than guess a recipe.
			h.log.Debug("imaged: base convergence: skip cached base", "key", baseKey, "err", err)
			continue
		}
		h.rememberStagedBase(ref, baseKey, sched.BaseDigestKeyForArch(runtime, arch))
		h.log.Info("imaged: base convergence: tracking cached base staged by an earlier daemon",
			"key", baseKey, "ref", ref)
	}
}
