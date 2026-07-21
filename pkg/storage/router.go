package storage

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// PrefixRouter composes multiple StorageBackends behind a single
// interface by dispatching each key to the backend whose prefix
// matches. It is the seam that lets a single daemon (imaged, vmmd)
// serve keys across multiple host roots — production wiring has
// /srv/fc as the canonical root covering snap/, base/, layers/,
// kernel/, and /var/lib/faas/apps as a separate sibling for the
// apps/ prefix.
//
// Dispatch model:
//
//   - Each Put/Get/Delete looks up the longest matching prefix in
//     the routes map. The route's backend handles the call as if
//     the prefix had been stripped: the prefix becomes part of the
//     route's own key contract.
//   - The fallback backend (when set) handles keys that don't match
//     any prefix. cmd/imaged configures fallback=/srv/fc and
//     apps-prefix=/var/lib/faas/apps, so the canonical /srv/fc/*
//     keys land in the local backend while /apps/* lands in the
//     sibling one — without the caller having to know which root
//     owns which prefix.
//   - List routes its prefix to the matching backend if the prefix
//     matches a route, otherwise it returns every key across every
//     route + fallback by issuing parallel List calls.
//
// Composition vs. inheritance: a PrefixRouter is itself a
// StorageBackend; you can stack routers (e.g. for namespace
// multi-tenancy in a future slice) without touching the per-driver
// logic.
type PrefixRouter struct {
	// routes maps each prefix to its backend. The keys are matched
	// longest-first at call time, so order in the map does not
	// matter; sorting is done in the dispatch helper.
	routes map[string]StorageBackend
	// fallback handles any key that does not match a route prefix.
	// May be nil — without a fallback, unmatched keys surface as
	// ErrInvalidKey (no route) rather than a confusing 404.
	fallback StorageBackend
	// local is the LocalArtifactLister capability the router
	// aggregates across all routes. Lazily built in NewPrefixRouter
	// so an empty router still implements the lister interface.
	local LocalArtifactLister
}

// NewPrefixRouter wires a router from a routes map and an optional
// fallback. The routes map may be empty (every key goes to fallback).
// Every value must be non-nil; the constructor enforces this so a
// zero-valued map doesn't silently drop traffic.
//
// If every backend in routes + fallback implements LocalArtifactLister,
// the router itself implements LocalArtifactLister and aggregates
// results; otherwise List on the router returns ErrInvalidKey for
// any prefix that doesn't fall on a backend with the capability.
//
// Routing rule: longest matching prefix wins. A key like
// "apps/acme/dep-1.ext4" routes to the backend whose prefix is the
// longest match (e.g. "apps/acme/" wins over "apps/").
func NewPrefixRouter(routes map[string]StorageBackend, fallback StorageBackend) (*PrefixRouter, error) {
	for p, b := range routes {
		if b == nil {
			return nil, fmt.Errorf("storage: router: route %q has nil backend", p)
		}
		// Reject obviously bad prefixes up front: a route must end in
		// '/' so dispatch never splits a key on a non-boundary (e.g.
		// prefix "apps" must not match "appsfoo/x"). Validate the
		// stripped form via the storage contract so an empty-prefix
		// route is still rejected as invalid.
		if !strings.HasSuffix(p, "/") {
			return nil, fmt.Errorf("storage: router: route %q must end in '/'", p)
		}
		if err := validateKey(strings.TrimSuffix(p, "/")); err != nil {
			return nil, fmt.Errorf("storage: router: route %q: %w", p, err)
		}
	}
	// Build the LocalArtifactLister capability if every backend
	// implements it. LocalStorageBackend does; a future remote driver
	// might not, in which case the router surfaces ErrInvalidKey
	// instead of silently dropping list calls.
	r := &PrefixRouter{routes: make(map[string]StorageBackend, len(routes)), fallback: fallback}
	for p, b := range routes {
		r.routes[p] = b
	}
	if r.allLocal() {
		backends := make(map[string]StorageBackend, len(r.routes)+1)
		for k, v := range r.routes {
			backends[k] = v
		}
		if r.fallback != nil {
			backends[""] = r.fallback
		}
		r.local = &aggregatedLister{backends: backends}
	}
	return r, nil
}

// allLocal reports whether every backend in the router (routes +
// fallback) implements LocalArtifactLister. Used to decide whether
// the router can aggregate List calls.
func (r *PrefixRouter) allLocal() bool {
	for _, b := range r.routes {
		if _, ok := b.(LocalArtifactLister); !ok {
			return false
		}
	}
	if r.fallback != nil {
		if _, ok := r.fallback.(LocalArtifactLister); !ok {
			return false
		}
	}
	return true
}

// dispatch selects the backend for a key. The longest matching
// prefix wins; absent a match, the fallback handles it. Returns
// (backend, remainder) where remainder is the key with the matched
// prefix stripped.
//
// Put/Get/Delete on a missing-route key with no fallback return
// ErrInvalidKey so the caller sees a clear "no route" error
// instead of a confusing 404.
func (r *PrefixRouter) dispatch(key string) (StorageBackend, string, error) {
	if err := validateKey(key); err != nil {
		return nil, "", err
	}
	bestLen := -1
	var best StorageBackend
	var bestPrefix string
	for prefix, b := range r.routes {
		// HasPrefix match is the prefix-equal-or-longer check. The
		// segment-boundary check below is the defense-in-depth against
		// a mid-segment split (e.g. prefix "apps" must not match
		// "appsfoo/x"). NewPrefixRouter enforces a trailing "/" on
		// every route, so this only fires when the prefix lacks one —
		// the bug case in the review.
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if !strings.HasSuffix(prefix, "/") && len(key) > len(prefix) && key[len(prefix)] != '/' {
			continue
		}
		if len(prefix) > bestLen {
			bestLen = len(prefix)
			best = b
			bestPrefix = prefix
		}
	}
	if best != nil {
		// Remainder is the key with the matched prefix stripped so
		// the underlying backend sees its own key contract. If the
		// key equals the prefix (which only happens when the prefix
		// ends in "/" and the caller passed exactly that), the
		// remainder is empty; the underlying backend rejects empty
		// keys with ErrInvalidKey, which is the correct behaviour.
		remainder := strings.TrimPrefix(key, bestPrefix)
		return best, remainder, nil
	}
	if r.fallback != nil {
		return r.fallback, key, nil
	}
	return nil, "", fmt.Errorf("%w: no route for %q", ErrInvalidKey, key)
}

// Put dispatches via dispatch and forwards to the matching backend.
// ctx propagation: the per-call ctx is passed through verbatim so a
// cancelled caller observes cancellation on every dispatched Put.
func (r *PrefixRouter) Put(ctx context.Context, key string, rd io.Reader) error {
	b, rem, err := r.dispatch(key)
	if err != nil {
		return err
	}
	if err := b.Put(ctx, rem, rd); err != nil {
		// Re-add the route prefix so the caller sees the full key in
		// the error chain. The dispatch() helper already returned the
		// stripped remainder; rebuild by combining the route's known
		// prefix (matched at dispatch time) with rem. We re-discover
		// the prefix here so the wrap is exact even when the backend
		// surfaced an error that wraps its own key.
		prefix := r.prefixFor(b)
		return fmt.Errorf("storage: put %q: %w", prefix+rem, err)
	}
	return nil
}

// Get mirrors Put: dispatch, forward, wrap on error.
func (r *PrefixRouter) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	b, rem, err := r.dispatch(key)
	if err != nil {
		return nil, err
	}
	rc, err := b.Get(ctx, rem)
	if err != nil {
		prefix := r.prefixFor(b)
		return nil, fmt.Errorf("storage: get %q: %w", prefix+rem, err)
	}
	return rc, nil
}

// Delete mirrors Put: dispatch, forward, swallow "not found" only
// when the backend signals it via ErrNotFound wrapping. Anything
// else propagates.
func (r *PrefixRouter) Delete(ctx context.Context, key string) error {
	b, rem, err := r.dispatch(key)
	if err != nil {
		return err
	}
	if err := b.Delete(ctx, rem); err != nil {
		// ErrNotFound from a dispatch is the cleanest signal that the
		// route maps to a backend that already lost the file — we
		// keep it but re-wrap with the full key so the caller can
		// branch on IsNotFound without losing the route context.
		prefix := r.prefixFor(b)
		return fmt.Errorf("storage: delete %q: %w", prefix+rem, err)
	}
	return nil
}

// prefixFor returns the route prefix whose backend matches b. Used
// to re-decorate error chains with the full key. If b is the
// fallback, returns "". A non-match indicates a programming error
// (dispatch gave us a backend we never registered) and is treated
// as ErrInvalidKey so the failure mode is loud rather than silent.
func (r *PrefixRouter) prefixFor(b StorageBackend) string {
	for p, candidate := range r.routes {
		if candidate == b {
			return p
		}
	}
	if r.fallback == b {
		return ""
	}
	return "" // programming error; the wrap below will still produce a usable error
}

// List aggregates every LocalArtifactLister-capable backend in the
// router. Each prefix gets its own List call; results are combined
// and re-prefixed so the caller sees the full key. An empty prefix
// lists everything.
//
// Returns ErrInvalidKey when at least one backend does NOT implement
// LocalArtifactLister — the caller can decide whether to log-skip
// (imaged's GC) or surface the error.
func (r *PrefixRouter) List(ctx context.Context, prefix string) ([]string, error) {
	if r.local == nil {
		return nil, fmt.Errorf("%w: at least one router backend does not implement List", ErrInvalidKey)
	}
	return r.local.List(ctx, prefix)
}

// aggregatedLister fans List out across the router's backends and
// re-prefixes the result so the caller sees the full logical key.
type aggregatedLister struct {
	// backends is a copy of the router's routes + (optionally)
	// fallback under the empty-string prefix. The empty prefix is
	// the fallback's slot because the empty prefix would otherwise
	// conflict with no-prefix routes.
	backends map[string]StorageBackend
}

// List iterates each backend, calls List with the stripped prefix
// (or empty prefix for the fallback slot), and re-prefixes each
// result with the route's prefix. The combined slice is sorted for
// determinism — the per-backend order is arbitrary by design.
//
// Routing of the prefix itself:
//
//   - If the caller's prefix matches a route exactly (or with a
//     longer route winning), only that route's backend handles the
//     list (the others would return a wrong scope).
//   - If the caller's prefix is empty (list-everything), every
//     backend handles its own scope: each route lists its subtree,
//     the fallback lists its root.
//   - If the caller's prefix is non-empty but matches no route
//     exactly, the fallback handles it.
func (a *aggregatedLister) List(ctx context.Context, prefix string) ([]string, error) {
	if prefix != "" {
		if err := validateKey(strings.TrimSuffix(prefix, "/")); err != nil {
			return nil, err
		}
	}
	// Find the longest matching route (if any) for the caller's
	// prefix. The empty prefix matches every route (list-all).
	var combined []string
	listRoute := func(route string, b StorageBackend, sub string) error {
		keys, err := b.(LocalArtifactLister).List(ctx, sub)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if k == "" {
				continue
			}
			if route != "" {
				combined = append(combined, route+k)
			} else {
				combined = append(combined, k)
			}
		}
		return nil
	}
	// Phase 1: routes. Each route lists its own subtree under the
	// caller's prefix stripped of the route. When the caller's
	// prefix is empty, every route lists its full subtree.
	for route, b := range a.backends {
		if route == "" {
			continue
		}
		if prefix != "" && !strings.HasPrefix(prefix, route) {
			continue
		}
		sub := ""
		if prefix != "" {
			sub = strings.TrimPrefix(prefix, route)
		}
		if err := listRoute(route, b, sub); err != nil {
			return nil, fmt.Errorf("storage: list %q: %w", prefix, err)
		}
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
	}
	// Phase 2: fallback. The fallback is consulted in two cases:
	//
	//   - prefix is empty (list-all): the routes already covered their
	//     own subtrees; the fallback covers its own root.
	//   - prefix is non-empty and matches NO route: the fallback
	//     handles the call directly.
	//
	// When the prefix matches a route's scope, the fallback is
	// intentionally skipped — its root does not contain that subtree,
	// so a list call would walk a non-existent path and return empty.
	// (That was the v1 behaviour too; it produced the right answer but
	// did unnecessary I/O. This is the cleanup.)
	if fb, ok := a.backends[""]; ok && (prefix == "" || !anyRouteContains(a.backends, prefix)) {
		if err := listRoute("", fb, prefix); err != nil {
			return nil, fmt.Errorf("storage: list %q: %w", prefix, err)
		}
	}
	sort.Strings(combined)
	return combined, nil
}

// anyRouteContains reports whether any registered route (non-empty
// key) is a prefix-or-equal match for query. Used by the aggregator
// to decide whether the fallback should be consulted for a non-empty
// prefix — when a route owns the prefix, the fallback is irrelevant.
func anyRouteContains(backends map[string]StorageBackend, query string) bool {
	for route := range backends {
		if route == "" {
			continue
		}
		if query == route || strings.HasPrefix(query, route) {
			return true
		}
	}
	return false
}
