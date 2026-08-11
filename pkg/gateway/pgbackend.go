package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// WarmHintFunc is the sticky-warm affinity source for the picker
// (placement scheduler PR, ADR-025). It returns the compute_node.id
// that last warmed the given app, or "" with found=false if no hint
// is available (no record, expired, or stream disconnected). A nil
// WarmHintFunc is treated as "no hint always" — the picker falls
// through to least-loaded headroom identically to a fresh install.
//
// The hint is bias, never a gate — pkg/sched/ChoosePlacement
// ignores the hint when the preferred node is saturated. ADR-005
// (cold boot must always work) is preserved at the gateway too:
// an empty hint degrades to round-robin-within-warmest-node.
type WarmHintFunc func(appID string) (nodeID string, found bool)

// Router is the Postgres-backed routing seam PGBackend reads through. It is the
// narrow slice of the state.Store the edge needs to resolve a hostname to its
// app; cmd/gatewayd-internal/adapts state.Store to it. Keeping it gateway-local (rather
// than importing pkg/state here) keeps the hot request path unit-testable with
// a fake and keeps this package's dependency surface to pkg/api only.
type Router interface {
	// ResolveHost maps a request hostname (lowercased, port-stripped) to its
	// routing app. ok=false means "no app is routed here" (a 404, not an error);
	// a non-nil error means the lookup itself failed (Postgres down) and the
	// caller should surface it as a 404 without caching.
	ResolveHost(ctx context.Context, host string) (app App, ok bool, err error)
}

// targetSet (issue #168, placement scheduler PR) is the per-deployment
// list of routable instances the gateway holds. Members are unique by
// InstanceID; Pick uses a per-node sub-cursor so the picker biases
// toward the node with the most healthy entries and applies a
// sticky-warm affinity bonus (ADR-025).
//
// PR-B (issue #556): one targetSet now corresponds to one *deployment*,
// not the whole app. The app-level picker (appPicker) holds a targetSet
// per live deployment and routes traffic to one of them by
// deployment.traffic_percent.
//
// Concurrency model:
//   - subCursors maps each distinct NodeID to an atomic round-robin
//     cursor. Pick increments the cursor of the winning node only,
//     so two concurrent picks for different nodes don't compete.
//   - entries is read-only inside Pick (RLock); mutation happens
//     under Lock (add / remove).
//   - nodeOrder is a stable insertion order for tie-breaking when
//     two nodes have equal healthy counts. Re-built lazily on add
//     if the new Target's NodeID is novel.
type targetSet struct {
	next       atomic.Uint64 // legacy single-cursor; retained so legacy callers / tests that read len + add keep working. pick() no longer increments it.
	entries    []Target
	nodeOrder  []string                  // stable node-id order for tie-break
	subCursors map[string]*atomic.Uint64 // nodeID -> per-node cursor
}

// add appends a new Target to the set, replacing any existing entry with
// the same InstanceID. Callers must hold tgtMu (Lock).
func (s *targetSet) add(t Target) {
	if t.NodeID == "" || t.InstanceID == "" {
		return
	}
	for i, e := range s.entries {
		if e.InstanceID == t.InstanceID {
			// Re-admission of a known instance — overwrite in place.
			s.entries[i] = t
			return
		}
	}
	s.entries = append(s.entries, t)
	if _, ok := s.subCursors[t.NodeID]; !ok {
		s.subCursors[t.NodeID] = &atomic.Uint64{}
		s.nodeOrder = append(s.nodeOrder, t.NodeID)
	}
}

// remove drops the entry whose InstanceID matches. Returns the new slice
// length. Callers must hold tgtMu (Lock).
func (s *targetSet) remove(instanceID string) int {
	var removedNode string
	for i, e := range s.entries {
		if e.InstanceID == instanceID {
			removedNode = e.NodeID
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			break
		}
	}
	if removedNode == "" {
		return len(s.entries)
	}
	// Lazy GC: only drop the per-node cursor when no entry remains
	// for that node. Keeps the picker hot-path free of allocations
	// for steady-state traffic.
	stillUsed := false
	for _, e := range s.entries {
		if e.NodeID == removedNode {
			stillUsed = true
			break
		}
	}
	if !stillUsed {
		delete(s.subCursors, removedNode)
		for i, n := range s.nodeOrder {
			if n == removedNode {
				s.nodeOrder = append(s.nodeOrder[:i], s.nodeOrder[i+1:]...)
				break
			}
		}
	}
	return len(s.entries)
}

// pick returns one Target via per-node sub-cursors with sticky-warm
// affinity. The picker:
//
//  1. Groups entries by NodeID.
//  2. Scores each node: score(node) = healthyCount(node) + warmBonus(node).
//     warmBonus is +∞ if warmHint == nodeID, else 0. The +∞ is
//     modelled as math.MaxInt so the tie-break stays integer-arithmetic
//     without reflection.
//  3. Round-robin within the winning node's sub-cursor.
//
// Callers must hold tgtMu (RLock). Empty set → ok=false.
//
// warmHint="" or warmHintFound=false → score reduces to healthyCount.
// The nodeOrder slice keeps the comparison deterministic when two
// nodes tie (legacy single-box deploys always have exactly one
// node, so this branch is exercised in tests, not production).
func (s *targetSet) pick(warmHint string) (Target, bool) {
	if len(s.entries) == 0 {
		return Target{}, false
	}
	if len(s.subCursors) == 1 {
		// Fast path: every entry is on one node. Round-robin
		// over the entries directly (the only sub-cursor we
		// have is for that one node). This keeps the single-box
		// hot path allocation-free, matching the legacy atomic
		// round-robin shape.
		idx := s.next.Add(1) - 1
		return s.entries[int(idx%uint64(len(s.entries)))], true
	}
	// Multi-node: group entries by node, score each, pick the
	// winning node, round-robin within it.
	counts := make(map[string]int, len(s.nodeOrder))
	for _, e := range s.entries {
		counts[e.NodeID]++
	}
	var (
		bestNode  string
		bestScore int64 = -1
	)
	for _, nodeID := range s.nodeOrder {
		c := int64(counts[nodeID])
		score := c
		if warmHint != "" && nodeID == warmHint {
			// +∞: bias the warm node above any non-warm node.
			// math.MaxInt is fine because counts stay bounded
			// by max_concurrency (≤ 20 for Scale plan).
			score = c + (1 << 62)
		}
		if score > bestScore {
			bestScore = score
			bestNode = nodeID
		} else if score == bestScore {
			// Stable lex tie-break so identical scores don't
			// flip the pick between calls. Cheap because
			// nodeOrder is small.
			if nodeID < bestNode {
				bestNode = nodeID
			}
		}
	}
	if bestNode == "" {
		return Target{}, false
	}
	cursor, ok := s.subCursors[bestNode]
	if !ok {
		return Target{}, false
	}
	// Build a per-node index slice on demand; the picker is on
	// the hot path so we cache the indices for repeated calls.
	// The simplest correct implementation just counts.
	n := counts[bestNode]
	idx := cursor.Add(1) - 1
	// Walk entries to find the idx-th one on bestNode.
	var seen int
	for _, e := range s.entries {
		if e.NodeID != bestNode {
			continue
		}
		if int(idx%uint64(n)) == seen {
			return e, true
		}
		seen++
	}
	return Target{}, false
}

// PGBackend is gatewayd-internal's production Backend (spec §4.1, issue #168): a
// host→app routing cache over Postgres plus schedd over gRPC for
// per-instance admission. Replaces the M4-era unwiredBackend once schedd's
// gRPC surface (ADR-018) is up.
//
// Two caches, populated on different paths:
//
//   - routes/apps: host→app_id (RouteCache, spec §4.1 10k LRU) and app_id→App
//     (plan). Filled on a Lookup miss via Router; wholesale-reset on an
//     app/domain change (Reset / FlushRoutes).
//   - targets: app_id → *targetSet. Filled by Admit (issue #168) when
//     schedd returns a fresh instance, and mutated by EvictInstance when
//     an instance_changed notification says a specific instance parked.
//     Pick is the ctx-less hot path, so it must be a pure in-memory read —
//     the notify loop + the admit path keep it fresh rather than per-request
//     DB hits.
//
// Phase 2 / Gate A: schedd resolution is per-app (apps.node_id).
// The PGBackend exposes WithAppResolver + WithClientForApp hooks so
// production wiring can hand it a per-node schedd client cache
// without changing the legacy Scheduler contract tests rely on.
// When the hooks are unset, Admit falls through to the legacy single
// schedd field — matching pre-Gate-A behaviour.
//
// PR-B (issue #556) — traffic splitting across deployments:
//
// The PGBackend's hot-path picker is per-app + per-deployment.
// apps maps appID → *appPicker. Each appPicker holds:
//
//   - weights []deploymentWeight, sorted (Percent DESC, DeploymentID ASC)
//     for stable tie-break on the cumulative-weight binary search.
//   - cum []int, the cumulative sum of weights (last entry is always 100
//     once PR-A's UpdateDeploymentTraffic has stamped Σ=100 on the row).
//   - cursor atomic.Uint64 — Pick increments it and binary-searches cum
//     with (cursor mod 100) as the slot index. O(log N) per request, where
//     N is the number of live deployments for that app (small — Pro+ canary
//     is bounded by a handful of slots per app).
//   - sets map[string]*targetSet — one per live deployment. Pick chooses
//     a deployment via the weighted stride, then hands off to its targetSet
//     for the per-node sub-cursor round-robin.
//
// Single-deployment fast path: when weights has length 1, Pick skips
// the weighted stride and goes straight to the sole targetSet's
// pick(warmHint) — byte-identical to the pre-PR-B behaviour. The
// backward-compat test (TestPGBackend_PickSingleDeployment_ByteIdenticalToLegacy)
// pins this so a pre-PR-A app sees zero behaviour change.
//
// When a request lands on a deployment with an empty targetSet (cold
// deployment, no admitted instances yet), the picker falls through
// to the largest-weight deployment's targetSet — biases warm
// deployments but does not deadlock the request. PR-C ships
// wake-fan-out to remove this fallback.
type PGBackend struct {
	router Router
	sched  Scheduler
	log    *slog.Logger

	routes *RouteCache // host -> app_id (LRU)

	appsMu sync.RWMutex
	apps   map[string]App // app_id -> App (plan)

	// warmHint is the sticky-warm affinity source for the picker
	// (placement scheduler PR, ADR-025). nil = no hint; pick() falls
	// through to per-node healthyCount scoring. Set via WithWarmHint.
	warmHint WarmHintFunc

	tgtMu sync.RWMutex
	// appsPicker (PR-B / issue #556) is the hot-path app_id → *appPicker cache.
	// Each appPicker holds one *targetSet per live deployment of the app,
	// plus the weighted-pick state. Pre-PR-B code used `targets
	// map[string]*targetSet`; the new shape preserves the per-instance
	// round-robin (targetSet.pick) while adding the per-deployment
	// weighted stride. PR-B keeps targetSet.pick unchanged.
	appsPicker map[string]*appPicker

	// store (PR-B / issue #556) is the gateway-side seam for the
	// RefreshDeploymentWeights notify handler. nil = no refresh; the
	// picker retains its initial weights until the daemon restarts.
	// Production wires this from cmd/gatewayd-internal so a deployment
	// row's traffic_percent change invalidates the in-memory cache
	// without restarting the edge. Tests inject a fake Store.
	store deploymentWeightsStore

	// appResolver (Phase 2 / Gate A) maps appID → state.App so the
	// per-node client cache can find apps.node_id without a second
	// store hop. Optional: nil falls through to the legacy single-sched
	// path. Production wires this to a closure that calls
	// state.Store.AppByID; tests can return a synthetic App.
	appResolver func(ctx context.Context, appID string) (App, bool, error)

	// clientForApp (Phase 2 / Gate A) returns the schedd client that
	// owns the given app. Mandatory when appResolver is set. Production
	// wires this to scheddRouter.ScheddForApp; tests inject a closure
	// that returns a static fake. Returning ok=false forces a fallback
	// to the legacy b.sched field — useful for tests that exercise the
	// single-sched path.
	clientForApp func(ctx context.Context, app App) (Scheduler, bool, error)

	// legacySingleBox (Phase 2 / Gate A) gates the resolveSched
	// fallback to the legacy b.sched field. When true, a missing
	// app row or empty NodeID falls through to b.sched — this is the
	// single-box posture where every app lives on the local schedd.
	// When false (the multi-box posture), the fallback is unsafe
	// because b.sched is the legacy default-local dial and a foreign
	// owner's app routed through it would return FailedPrecondition,
	// surfacing as a 503 storm on transient cache misses. Multi-box
	// startup sets this to false; single-box startup sets it to true.
	// The setter (WithLegacySingleBox) is wired by cmd/gatewayd-internal's
	// startup phase after it has resolved fleet posture from the
	// compute_nodes table.
	legacySingleBox bool

	// publicAuthCache is the unsealed basic-auth credential
	// cache (issue #477 / ADR-079). nil = no caching; the
	// basic-auth path falls back to per-request unsealing
	// (slower but safe). Production wires it from
	// cmd/gatewayd-internal so the 60s TTL + per-key
	// invalidation through db.NotifyKeyChanged both apply.
	publicAuthCache *PublicAuthCache
	// edgeRules (ADR-089 / issue #561 PR 3) is the
	// per-host edge-rule matcher the invalidator drives.
	// nil = no matcher wired (default; pre-PR-3 behaviour
	// preserved). cmd/gatewayd-internal wires it via
	// WithEdgeRules next to WithPublicAuthCache so the
	// db.NotifyEdgeRuleChanged subscriber in
	// cmd/gatewayd-internal/backend.go can call Reset()
	// on it. ResetEdgeRules is nil-safe so the
	// subscriber never has to branch.
	edgeRules EdgeRuleMatcher
}

// AppResolverFunc is the typed alias for WithAppResolver. Mirrors
// Router.ResolveHost so the wire-up is symmetric.
type AppResolverFunc func(ctx context.Context, appID string) (App, bool, error)

// ClientForAppFunc is the typed alias for WithClientForApp.
type ClientForAppFunc func(ctx context.Context, app App) (Scheduler, bool, error)

// WithAppResolver sets the appID → state.App hook used by Admit to
// find apps.node_id (Phase 2 / Gate A). nil clears the hook —
// production wires a closure that calls state.Store.AppByID; tests
// pass an in-memory map lookup.
func (b *PGBackend) WithAppResolver(fn AppResolverFunc) *PGBackend {
	b.appResolver = fn
	return b
}

// WithClientForApp sets the per-app schedd client factory used by
// Admit to find the owner schedd (Phase 2 / Gate A). nil clears
// the hook — production wires a closure that calls
// scheddRouter.ScheddForApp. Tests pass an in-memory map lookup.
//
// The factory returns (client, ok, err): ok=true means the hook
// produced a client and Admit should use it; ok=false (with nil
// err) means the hook can't resolve (no node_id / no row) and Admit
// should fall back to the legacy b.sched field; a non-nil err means
// the hook is configured but the resolution failed, and Admit
// surfaces the error.
func (b *PGBackend) WithClientForApp(fn ClientForAppFunc) *PGBackend {
	b.clientForApp = fn
	return b
}

// WithWarmHint attaches the sticky-warm affinity source for the picker.
// nil is tolerated (the picker degrades to per-node healthyCount).
// cmd/gatewayd-internal/wires this from the WarmHint stream that schedd exposes
// via the gRPC surface; tests pass a closure that reads from a fake
// or a fixed map.
//
// As of PR #429 the WarmHint stream gRPC RPC is not yet wired, so
// production gateways leave this unset. The picker correctly returns
// "no hint" and falls through to per-node healthyCount + lex
// tie-break on nodeOrder. Sticky-warm is enabled as soon as the
// stream consumer lands (follow-up slice tracked in
// docs/adr/025 — see plan file).
func (b *PGBackend) WithWarmHint(fn WarmHintFunc) *PGBackend {
	b.warmHint = fn
	return b
}

// WithLegacySingleBox toggles the resolveSched fallback. Single-box
// deployments (one schedd, every app owned by default-local) want
// the legacy fallback to remain in effect so transient cache misses
// do not deny traffic — there's only one schedd to dial, and the
// ownership guard never trips because every app's NodeID matches.
// Multi-box deployments (N schedds, per-app ownership) MUST set this
// to false: the fallback would otherwise route a foreign-owned app
// through the legacy default-local dial, and that schedd returns
// FailedPrecondition, surfacing as a 503 storm on transient cache
// misses. The setter is documented at the field (see legacySingleBox
// above). Returns b so the gatewayd-internal startup wire-up can chain.
func (b *PGBackend) WithLegacySingleBox(v bool) *PGBackend {
	b.legacySingleBox = v
	return b
}

// compile-time assertion PGBackend satisfies the edge seam.
var _ Backend = (*PGBackend)(nil)

// NewPGBackend wires the production backend. log may be nil (slog default).
func NewPGBackend(router Router, sched Scheduler, log *slog.Logger) *PGBackend {
	if log == nil {
		log = slog.Default()
	}
	return &PGBackend{
		router:     router,
		sched:      sched,
		log:        log,
		routes:     NewRouteCache(RouteCacheCap),
		apps:       map[string]App{},
		appsPicker: map[string]*appPicker{},
	}
}

// appPicker (PR-B / issue #556) is the per-app picker state the
// gateway holds when traffic is split across N deployments. One app
// → one appPicker → N *targetSets keyed by deployment ID.
//
// The picker algorithm:
//
//  1. Pick a deployment via weighted stride: cursor.Add(1) mod 100
//     gives the slot index; binary-search cum for the smallest i with
//     cum[i] > slot. cum is the cumulative-weight array (last entry
//     = 100 once UpdateDeploymentTraffic stamps Σ=100).
//  2. Look up the chosen targetSet. If empty (cold deployment), fall
//     through to the largest-weight deployment's targetSet — biases
//     warm deployments but never deadlocks the request.
//  3. Hand off to the targetSet's per-node sub-cursor round-robin
//     (issue #168 + ADR-025 — unchanged).
//
// Concurrency: cursor is the only mutable atomic; weights and cum are
// rebuilt atomically by RefreshDeploymentWeights under tgtMu.Lock.
// targetSet instances live inside sets and are protected by their own
// mu (b.tgtMu).
type appPicker struct {
	weights []deploymentWeight
	cum     []int                 // cum[i] = Σ_{j≤i} weights[j].Percent; cum[len-1] = 100
	cursor  atomic.Uint64         // Pick increments; (cursor-1) mod 100 is the slot
	sets    map[string]*targetSet // deploymentID → targetSet
}

// deploymentWeight is the per-deployment row of the picker's
// weight table (PR-B / issue #556). Percent is the live traffic
// share (0–100, Σ=100 within an app, enforced under PR-A's
// UpdateDeploymentTraffic FOR UPDATE lock).
type deploymentWeight struct {
	DeploymentID string
	Percent      int
}

// deploymentWeightsStore is the gateway-side seam used by
// RefreshDeploymentWeights (PR-B / issue #556). It is the narrow
// slice of state.Store the picker needs; production wires
// pkg/state.PgStore, tests inject a fake. Declared gateway-local so
// the picker has no pkg/state import — same shape as Router above.
type deploymentWeightsStore interface {
	LiveDeployments(ctx context.Context, appID string) ([]DeploymentWeightsRow, error)
}

// DeploymentWeightsRow is the gateway-side projection of the
// live-deployment row the picker needs (PR-B / issue #556). The
// gateway does not import pkg/state, so the production wiring
// adapter (cmd/gatewayd-internal/main.go) translates from
// state.Deployment to this shape. Only fields the picker reads are
// exposed: the live deployment's ID and its traffic percent.
type DeploymentWeightsRow struct {
	ID             string
	TrafficPercent int
}

// RouteCacheCap is the host→app_id cache ceiling (spec §4.1: 10,000 routes).
const RouteCacheCap = 10_000

// Lookup resolves a hostname to its app, cache-first (spec §4.1). A cache miss
// is one indexed Postgres lookup through the Router; the result is memoized in
// both the route (host→app_id) and app (app_id→plan) caches. A Router error or
// an unknown host both yield ok=false so the handler writes a 404.
func (b *PGBackend) Lookup(ctx context.Context, host string) (App, bool) {
	if appID, ok := b.routes.Get(host); ok {
		if app, ok := b.getApp(appID); ok {
			return app, true
		}
	}
	app, ok, err := b.router.ResolveHost(ctx, host)
	if err != nil {
		b.log.Warn("gateway: route lookup failed", "host", host, "err", err)
		return App{}, false
	}
	if !ok {
		return App{}, false
	}
	b.routes.Put(host, app.ID)
	b.putApp(app)
	return app, true
}

// Pick returns one routable Target for appID via the per-app
// weighted picker (PR-B / issue #556). Returns ("", false) when
// the cache is empty (no wake has populated it yet, every cached
// instance was evicted, or the app has no live deployments).
// The handler must ensure capacity before calling Pick so this
// only returns false on the rare eviction race.
//
// Algorithm:
//
//  1. Look up the appPicker. nil or empty weights → false.
//  2. Single-deployment fast path: skip the weighted stride and
//     hand off to the sole targetSet's per-node sub-cursor
//     (byte-identical to pre-PR-B).
//  3. Multi-deployment: cursor.Add(1) mod 100 → slot, binary
//     search cum for the chosen deployment. If that targetSet is
//     empty (cold deployment), fall through to the largest-weight
//     deployment's targetSet (PR-C ships wake-fan-out to remove
//     the fallback).
//
// Sticky-warm affinity (ADR-025) applies per-deployment inside
// the targetSet (unchanged from issue #168).
// PickResult (issue #556 / PR-C) widens the legacy (Target, bool)
// tuple with two signals the handler needs to drive wake-fan-out:
//
//   - Picked: the deploymentID the weighted stride landed on. May be
//     empty when no live deployment is known (empty weights). Set on
//     every success path (warm single-dep, warm multi-dep, AND cold
//     multi-dep where the fallback is the same bucket).
//   - ColdBucket: when non-empty, signals "the bucket we landed on
//     has no routable Targets". Set ONLY on the cold multi-dep
//     fallback path. The handler uses this to decide whether to call
//     Backend.Admit(ctx, appID, ColdBucket, max) and retry Pick.
//
// On the legacy single-deployment fast path (byte-identical to
// pre-PR-B), Picked=weights[0].DeploymentID and ColdBucket="".
//
// PR-B's pre-existing behaviour is preserved: OK=false on empty
// cache / picker miss (the handler maps this to
// api.ErrAppConcurrencyReached — the WakeGate will retry). PR-C
// adds the signal tuple but does NOT change the failure semantics.
type PickResult struct {
	Target     Target
	OK         bool
	Picked     string
	ColdBucket string
}

func (b *PGBackend) Pick(appID string) PickResult {
	var warmHint string
	if b.warmHint != nil {
		if n, found := b.warmHint(appID); found {
			warmHint = n
		}
	}
	b.tgtMu.RLock()
	picker := b.appsPicker[appID]
	if picker == nil || len(picker.weights) == 0 {
		b.tgtMu.RUnlock()
		return PickResult{}
	}
	// Single-deployment fast path: byte-identical to pre-PR-B.
	if len(picker.weights) == 1 {
		set := picker.sets[picker.weights[0].DeploymentID]
		if set == nil {
			b.tgtMu.RUnlock()
			return PickResult{}
		}
		t, ok := set.pick(warmHint)
		b.tgtMu.RUnlock()
		if !ok {
			return PickResult{}
		}
		return PickResult{Target: t, OK: true, Picked: picker.weights[0].DeploymentID}
	}
	// Multi-deployment weighted stride.
	slot := int(picker.cursor.Add(1)-1) % 100
	chosen := picker.weights[0].DeploymentID // safe fallback if binary search misses
	// Binary search cum for smallest i with cum[i] > slot.
	lo, hi := 0, len(picker.cum)
	for lo < hi {
		mid := (lo + hi) / 2
		if picker.cum[mid] > slot {
			chosen = picker.weights[mid].DeploymentID
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	set := picker.sets[chosen]
	if set == nil || len(set.entries) == 0 {
		// Cold deployment — fall through to the largest-weight
		// deployment's targetSet. Find by max Percent.
		//
		// PR-C: signal the handler via ColdBucket=chosen so it can
		// admit an instance on `chosen` and retry Pick. The
		// fallback below keeps the legacy "warmest bucket" path
		// operational during the rollout window (before any
		// wake-fan-out has run).
		var (
			fallbackID  string
			fallbackSet *targetSet
			best        int
		)
		for _, w := range picker.weights {
			s := picker.sets[w.DeploymentID]
			if s == nil || len(s.entries) == 0 {
				continue
			}
			if w.Percent > best || fallbackSet == nil {
				best = w.Percent
				fallbackID = w.DeploymentID
				fallbackSet = s
			}
		}
		if fallbackSet == nil {
			b.tgtMu.RUnlock()
			return PickResult{Picked: chosen, ColdBucket: chosen}
		}
		t, ok := fallbackSet.pick(warmHint)
		b.tgtMu.RUnlock()
		if !ok {
			return PickResult{Picked: chosen, ColdBucket: chosen}
		}
		t.DeploymentID = fallbackID
		return PickResult{Target: t, OK: true, Picked: chosen, ColdBucket: chosen}
	}
	t, ok := set.pick(warmHint)
	b.tgtMu.RUnlock()
	if !ok {
		return PickResult{Picked: chosen, ColdBucket: chosen}
	}
	t.DeploymentID = chosen
	return PickResult{Target: t, OK: true, Picked: chosen}
}

// HealthyCount returns the number of routable Targets currently
// cached for appID across all live deployments (PR-B /
// issue #556). Drives the WakeGate's shouldWake predicate: stop
// admitting once we're at the plan's effective max_concurrency.
// Σ over deployments — splitting traffic across N deployments
// does NOT double-bill the cap (issue #556 acceptance #3, §6.2-2).
func (b *PGBackend) HealthyCount(appID string) int {
	b.tgtMu.RLock()
	picker := b.appsPicker[appID]
	if picker == nil {
		b.tgtMu.RUnlock()
		return 0
	}
	n := 0
	for _, set := range picker.sets {
		n += len(set.entries)
	}
	b.tgtMu.RUnlock()
	return n
}

// Admit asks schedd to admit ONE additional instance for appID
// (issue #168). On the admitted path the new Target is added to
// the per-deployment targetSet keyed by deploymentID (PR-B /
// issue #556). On the at-capacity path the engine's typed result
// is passed through. On a real failure (RAM headroom, chooser,
// store) the error is preserved.
//
// PR-B: the cap check now sums across all per-deployment
// targetSets (issue #556 acceptance #3) so a burst cannot
// over-admit across deployments. Concurrent callers serialize on
// tgtMu. The Target is stamped with DeploymentID from the wire so
// the picker can route subsequent requests to the same bucket.
//
// method is the wake-outcome schedd actually performed (ADR-028).
// On the admitted path the value is WakeMethodSnapshotRestore or
// WakeMethodColdBoot; on at-capacity and error paths it is
// WakeMethodUnspecified.
//
// Phase 2 / Gate A: when WithAppResolver + WithClientForApp are
// configured, Admit first resolves the owning schedd via
// apps.node_id. Otherwise it falls through to the legacy single
// b.sched field.
func (b *PGBackend) Admit(ctx context.Context, appID, deploymentID string, maxConcurrency int) (string, WakeMethod, bool, error) {
	// Cheap fast path: refuse before we spend a gRPC round-trip.
	// Σ over all per-deployment targetSets (issue #556 acceptance #3).
	b.tgtMu.Lock()
	picker := b.appsPicker[appID]
	if picker != nil {
		total := 0
		for _, set := range picker.sets {
			total += len(set.entries)
		}
		if total >= maxConcurrency {
			b.tgtMu.Unlock()
			return "", WakeMethodUnspecified, true, nil
		}
	}
	b.tgtMu.Unlock()

	sched, err := b.resolveSched(ctx, appID)
	if err != nil {
		return "", WakeMethodUnspecified, false, err
	}

	// PR-C wake-fan-out: forward the per-deployment hint to
	// schedd so the new instance is admitted on the specific
	// live deployment the picker landed on. Empty falls through
	// to schedd's default (newest live deployment) — the legacy
	// single-deployment path.
	//
	// ADR-095: replaced AdmitInstance with EnsureWake so every
	// concurrent wake landing on this gateway for the same parked
	// app coalesces into one virtual boot on the schedd side. The
	// in-process WakeGate above still pre-filters by live
	// instances (it remains a cache in front of the authority),
	// but the authority now lives on schedd.
	instanceID, nodeID, returnedDeploymentID, wakeID, rawMethod, port, err := sched.EnsureWake(ctx, appID)
	if err != nil {
		return "", WakeMethodUnspecified, false, err
	}
	// Use the deploymentID schedd actually used (matches the
	// bucket the picker will route to); fall through to the
	// caller's hint if schedd returned "".
	if returnedDeploymentID != "" {
		deploymentID = returnedDeploymentID
	}
	method := scheddWakeMethodToGateway(rawMethod)
	// EnsureWake's leader runs Engine.Wake which honours the
	// per-app max_concurrency ledger; a follower that arrives
	// after the leader fills the last slot still sees a
	// successful boot pointing at that slot — the WakeGate
	// fast path above + the leader's ledger together close
	// the at-capacity loop.
	if nodeID == "" || instanceID == "" {
		// Empty IDs from EnsureWake: the per-app leader's ledger
		// refused the admit (already at max_concurrency or no
		// live deployment). Surface as the typed at-capacity
		// outcome the gateway treats as a benign no-op — the
		// WakeGate pre-filter and the leader's ledger together
		// close the at-cap loop.
		return "", WakeMethodUnspecified, true, nil
	}
	// Pre-PR-B fallback: a pre-PR-B schedd returns deploymentID="".
	// The picker collapses to single-targetSet behaviour when there
	// is no appPicker yet OR when the chosen deploymentID is not in
	// the picker. We use a sentinel "_" key for the legacy bucket
	// so the picker stays byte-identical with a single live row.
	bucket := deploymentID
	if bucket == "" {
		bucket = "_legacy"
	}
	b.tgtMu.Lock()
	picker = b.appsPicker[appID]
	if picker == nil {
		// Lazy-create a picker. If the store is wired, the
		// notify path seeds weights via RefreshDeploymentWeights
		// on the next deployment_changed event. If the store is
		// NOT wired (legacy single-box posture, or tests without
		// a state.Store seam), Admit synthesizes an implicit
		// single-deployment weight entry below so Pick routes
		// correctly without waiting for a refresh.
		picker = &appPicker{sets: map[string]*targetSet{}}
		b.appsPicker[appID] = picker
	}
	set := picker.sets[bucket]
	if set == nil {
		set = &targetSet{
			subCursors: map[string]*atomic.Uint64{},
		}
		picker.sets[bucket] = set
	}
	// Synthesize an implicit 100% weight ONLY when the picker has
	// no weight table at all (the legacy pre-PR-B single-bucket
	// posture, or a fresh test without a state.Store seam). Once
	// the picker knows about multiple deployments we MUST NOT
	// clobber the table — a missing-bucket admit on a multi-dep
	// app is a wake-fan-out signal (handled in PickResult.
	// ColdBucket), not an implicit-100 override.
	if len(picker.weights) == 0 {
		picker.weights = []deploymentWeight{{DeploymentID: bucket, Percent: 100}}
		picker.cum = []int{100}
	}
	set.add(Target{
		NodeID:       nodeID,
		InstanceID:   instanceID,
		WakeID:       wakeID,
		AddedAt:      time.Now(),
		Port:         port,
		DeploymentID: deploymentID,
	})
	b.tgtMu.Unlock()
	return wakeID, method, false, nil
}

// EvictInstance drops a specific instance from its app's per-deployment
// targetSets (PR-B / issue #556). The instance_changed notification
// loop calls this with the instance_id from the pg_notify payload;
// only that single entry is removed across all per-deployment
// buckets, leaving any other instances routable.
//
// Walks every per-deployment targetSet under the app's picker —
// we don't know which bucket owns the instance without an index.
// If the picker is empty after the walk, the picker is dropped.
// Per-bucket targetSets are preserved even when their entries
// become empty (cold deployment under non-zero weight); only the
// picker-level drop happens here. The per-deployment weight
// removal is RefreshDeploymentWeights's job — that's where we
// learn a deployment is no longer 'live'.
func (b *PGBackend) EvictInstance(appID, instanceID string) {
	if appID == "" || instanceID == "" {
		return
	}
	b.tgtMu.Lock()
	picker := b.appsPicker[appID]
	if picker == nil {
		b.tgtMu.Unlock()
		return
	}
	totalRemaining := 0
	for _, set := range picker.sets {
		totalRemaining += set.remove(instanceID)
	}
	if totalRemaining == 0 {
		delete(b.appsPicker, appID)
	}
	b.tgtMu.Unlock()
}

// EvictTarget drops ALL cached targets for appID (legacy contract).
// Kept for callers that don't yet parse the instance_id from the
// instance_changed payload — under-evicts nothing because the next
// request will Pick from what's left and miss if everything's gone,
// then re-admit. New code should prefer EvictInstance.
func (b *PGBackend) EvictTarget(appID string) {
	b.tgtMu.Lock()
	delete(b.appsPicker, appID)
	b.tgtMu.Unlock()
}

// RefreshDeploymentWeights (PR-B / issue #556) reloads the
// per-deployment weight table for appID from the store. The
// notify loop calls this on db.NotifyDeploymentChanged so a
// `faas traffic set --percent 25` takes effect within ~1s
// without restarting the edge.
//
// Behaviour:
//
//   - Reads LiveDeployments(appID). Empty slice → drop the picker
//     entirely (no live deployments, 503).
//   - Builds a new weights slice filtered to Percent > 0, sorted
//     (Percent DESC, DeploymentID ASC) for stable tie-break on the
//     cumulative-weight binary search.
//   - Rebuilds cum so cum[len-1] = 100. PR-A's UpdateDeploymentTraffic
//     enforces Σ=100 under FOR UPDATE; the test data may carry
//     a deployment with Percent=0 (sibling of a 100% primary),
//     which we filter out.
//   - Existing per-deployment targetSets are preserved. Instances
//     stay routable through the picker; only the weight table
//     flips.
//
// Returns the error wrapped with operation context. The notify
// handler logs-and-continues on a non-nil error — a brief
// staleness window is preferable to crashing the edge.
func (b *PGBackend) RefreshDeploymentWeights(ctx context.Context, appID string) error {
	if b.store == nil {
		// No store wired — nothing to refresh. The picker
		// retains its initial weights until the daemon
		// restarts. Production always wires the store from
		// cmd/gatewayd-internal so this branch is a test seam.
		return nil
	}
	rows, err := b.store.LiveDeployments(ctx, appID)
	if err != nil {
		return fmt.Errorf("gatewayd-internal: refresh deployment weights app=%s: %w", appID, err)
	}
	next := buildDeploymentWeights(rows)
	b.tgtMu.Lock()
	defer b.tgtMu.Unlock()
	if len(next) == 0 {
		delete(b.appsPicker, appID)
		return nil
	}
	picker, ok := b.appsPicker[appID]
	if !ok {
		picker = &appPicker{sets: map[string]*targetSet{}}
		b.appsPicker[appID] = picker
	}
	picker.weights = next
	picker.cum = buildCumulativeWeights(next)
	// Existing per-deployment targetSets in picker.sets are
	// preserved — instances stay routable through the picker.
	//
	// PR-C stale-set pruning: if a deployment is no longer in
	// the live weight table AND its targetSet is empty, drop it
	// from picker.sets. Without this prune, a deployment that
	// has been superseded would linger in picker.sets and
	// HealthyCount would over-count on the empty-set branch
	// (sets are walked by len(set.entries) but stale entries
	// stay until the next instance_changed notification — and a
	// deployment that's been superseded will never produce
	// another instance_changed). Empty-set + not-in-weights is
	// the precise signal that the deployment is gone.
	for id, set := range picker.sets {
		if len(set.entries) == 0 && !pickerHasDeploymentByID(next, id) {
			delete(picker.sets, id)
		}
	}
	return nil
}

// buildDeploymentWeights filters rows to Percent > 0 and sorts
// (Percent DESC, DeploymentID ASC) for stable tie-break on the
// cumulative-weight binary search (PR-B / issue #556). A
// deployment with Percent == 0 is filtered out: the picker must
// never route to it (caller treats that as "no live deployment,
// 503" rather than a cold fallback).
//
// The slice is small (Pro+ canary is bounded by a handful of
// deployments per app), so an O(N log N) sort is fine. Test seam.
func buildDeploymentWeights(rows []DeploymentWeightsRow) []deploymentWeight {
	out := make([]deploymentWeight, 0, len(rows))
	for _, r := range rows {
		if r.TrafficPercent <= 0 {
			continue
		}
		out = append(out, deploymentWeight{
			DeploymentID: r.ID,
			Percent:      r.TrafficPercent,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Percent != out[j].Percent {
			return out[i].Percent > out[j].Percent
		}
		return out[i].DeploymentID < out[j].DeploymentID
	})
	return out
}

// buildCumulativeWeights builds the cumulative-weight array the
// picker binary-searches. cum[i] = Σ_{j≤i} weights[j].Percent.
// Σ within an app is 100 by construction (PR-A's
// UpdateDeploymentTraffic enforces it under FOR UPDATE); a
// production row set whose Σ is something else (test data,
// migration in flight) still works — the search returns the
// last index when slot ≥ cum[len-1].
func buildCumulativeWeights(weights []deploymentWeight) []int {
	cum := make([]int, len(weights))
	var sum int
	for i, w := range weights {
		sum += w.Percent
		cum[i] = sum
	}
	return cum
}

// pickerHasDeploymentByID is the slice-level predicate (PR-C
// stale-set prune). Walks the supplied weight table — caller
// passes either p.weights or a freshly-rebuilt next slice.
func pickerHasDeploymentByID(weights []deploymentWeight, id string) bool {
	for _, w := range weights {
		if w.DeploymentID == id {
			return true
		}
	}
	return false
}

// WithStore (PR-B / issue #556) arms the deployment-weights store
// for RefreshDeploymentWeights. nil = no store wired; the picker
// retains its initial weights until the daemon restarts (test
// seam). Production wires this from cmd/gatewayd-internal so a
// db.NotifyDeploymentChanged event triggers the refresh.
//
// The setter returns *PGBackend for fluent chaining (same shape
// as every other PGBackend.With*).
func (b *PGBackend) WithStore(s deploymentWeightsStore) *PGBackend {
	b.store = s
	return b
}

// FlushRoutes clears the host→app and app→plan caches. gatewayd-internal calls this on
// an app_changed / domain_changed notification so a renamed slug, plan change,
// or deleted app is re-resolved (or 404'd) on the next request.
func (b *PGBackend) FlushRoutes() {
	b.routes.Reset()
	b.appsMu.Lock()
	b.apps = map[string]App{}
	b.appsMu.Unlock()
}

// InvalidatePublicAuth (issue #477 / ADR-079) drops every
// entry in the per-app basic-auth unsealed-credential cache.
// gatewayd-internal calls this on a db.NotifyKeyChanged notification
// (cmd/gatewayd-internal/backend.go) so a key rotation
// re-unseals on the next request. nil-safe: an unwired
// cache is a no-op.
func (b *PGBackend) InvalidatePublicAuth() {
	if b.publicAuthCache == nil {
		return
	}
	b.publicAuthCache.InvalidateAll()
}

// WithPublicAuthCache (issue #477 / ADR-079) arms the
// unsealed basic-auth credential cache. nil = no caching
// (the basic-auth path unseals per-request — slower but
// correct; tests prefer this). Production wires the
// gateway.NewPublicAuthCache() constructed in
// cmd/gatewayd-internal/main.go so the 60s TTL applies.
// The setter returns *PGBackend for fluent chaining
// (same shape as every other PGBackend.With*).
func (b *PGBackend) WithPublicAuthCache(cache *PublicAuthCache) *PGBackend {
	b.publicAuthCache = cache
	return b
}

// WithEdgeRules (ADR-089 / issue #561 PR 3) arms the
// per-host edge-rule matcher the invalidator resets on
// db.NotifyEdgeRuleChanged. matcher may be nil (matcher
// disabled; pre-PR-3 behaviour preserved). The setter
// returns *PGBackend for fluent chaining (same shape as
// every other PGBackend.With*).
func (b *PGBackend) WithEdgeRules(matcher EdgeRuleMatcher) *PGBackend {
	b.edgeRules = matcher
	return b
}

// ResetEdgeRules (ADR-089 / issue #561 PR 3) drops the
// edge-rule matcher's per-host LRU. cmd/gatewayd-internal
// calls this on db.NotifyEdgeRuleChanged (cmd/gatewayd-internal/backend.go
// handleInvalidation switch arm). nil-safe: an unwired
// matcher is a no-op so the notify subscriber never has
// to branch on a wiring check.
func (b *PGBackend) ResetEdgeRules() {
	if b.edgeRules == nil {
		return
	}
	b.edgeRules.Reset()
}

func (b *PGBackend) getApp(appID string) (App, bool) {
	b.appsMu.RLock()
	app, ok := b.apps[appID]
	b.appsMu.RUnlock()
	return app, ok
}

func (b *PGBackend) putApp(app App) {
	b.appsMu.Lock()
	b.apps[app.ID] = app
	b.appsMu.Unlock()
}

// resolveSched picks the schedd client that should service appID
// (Phase 2 / Gate A). Returns the per-node client when both hooks
// are configured AND the app has a non-empty NodeID; otherwise
// either falls through to the legacy single b.sched field
// (legacy single-box posture, gated by WithLegacySingleBox) or
// returns a definitive error (multi-box posture, where the
// fallback would route a foreign-owned app through the wrong
// schedd and surface a FailedPrecondition storm).
//
// A nil error and a nil Scheduler means the hook declined —
// caller falls back. b.legacySingleBox gates the fallback: when
// false, any of the three fallback triggers (resolver ok=false,
// app.NodeID empty, clientForApp ok=false) returns an error so
// the gateway surfaces a 503 with a useful message rather than
// a silent FailedPrecondition.
func (b *PGBackend) resolveSched(ctx context.Context, appID string) (Scheduler, error) {
	if b.appResolver != nil && b.clientForApp != nil {
		app, ok, err := b.appResolver(ctx, appID)
		if err != nil {
			return nil, err
		}
		if !ok {
			// App row missing. On single-box this is the
			// legacy fallback (b.sched serves every app);
			// on multi-box it's a 503 because routing to
			// b.sched would surface FailedPrecondition for
			// every foreign-owned app on transient miss.
			if b.legacySingleBox {
				return b.sched, nil
			}
			return nil, fmt.Errorf("gatewayd-internal: app %s: not found (transient resolver miss; multi-box posture forbids legacy fallback)", appID)
		}
		if app.NodeID == "" {
			// Pre-migration row or test fixture: only valid
			// in single-box where every app lives on the
			// local schedd.
			if b.legacySingleBox {
				return b.sched, nil
			}
			return nil, fmt.Errorf("gatewayd-internal: app %s has empty NodeID (pre-migration row; multi-box posture forbids legacy fallback)", appID)
		}
		cli, ok, err := b.clientForApp(ctx, app)
		if err != nil {
			return nil, err
		}
		if !ok {
			if b.legacySingleBox {
				return b.sched, nil
			}
			return nil, fmt.Errorf("gatewayd-internal: app %s (node %s): client resolver declined (transient miss; multi-box posture forbids legacy fallback)", appID, app.NodeID)
		}
		return cli, nil
	}
	return b.sched, nil
}
