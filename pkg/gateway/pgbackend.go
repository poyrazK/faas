package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
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
// the request picker itself uses global target round-robin; the hint is
// retained for compatibility with the scheduler-affinity event stream.
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
// InstanceID; Pick uses one global cursor so a multi-node target set
// actually fans traffic out across every routable instance. Warm affinity
// remains a scheduler placement hint; it must not turn a healthy multi-node
// fleet into a single-node routing bottleneck.
//
// PR-B (issue #556): one targetSet now corresponds to one *deployment*,
// not the whole app. The app-level picker (appPicker) holds a targetSet
// per live deployment and routes traffic to one of them by
// deployment.traffic_percent.
//
// Concurrency model:
//   - subCursors and nodeOrder are retained for mutation compatibility and
//     existing cache metadata; the request picker uses the global `next`
//     cursor so different nodes participate in the same rotation.
//   - entries is read-only inside Pick (RLock); mutation happens
//     under Lock (add / remove).
//   - nodeOrder and subCursors are maintained for cache mutation compatibility
//     and older introspection; they do not affect request selection.
type targetSet struct {
	next       atomic.Uint64 // global request round-robin cursor.
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

// pick returns one Target via global round-robin. Every target in the set is
// already routable and has passed the scheduler's capacity checks, so routing
// by target gives each admitted instance a share of the burst. In particular,
// equal-sized nodes must not resolve to one stable lexicographic winner.
//
// Callers must hold tgtMu (RLock). Empty set → ok=false. warmHint is retained
// in the signature for compatibility with the picker call sites; affinity is
// deliberately not applied at request routing time.
func (s *targetSet) pick(_ string) (Target, bool) {
	if len(s.entries) == 0 {
		return Target{}, false
	}
	idx := s.next.Add(1) - 1
	return s.entries[int(idx%uint64(len(s.entries)))], true
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

	// certIssuer (ADR-100 / issue #879) is the per-surface
	// cert-remint seam. nil = feature dark; the notify subscriber
	// no-ops so a misconfigured rollout can't crash the edge.
	// Production wires this from cmd/gatewayd-internal so a
	// tenant_surface_changed notification triggers a fresh SAN
	// mint; tests inject a fake that records the call.
	certIssuer CertIssuer

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

	// liveTargetLoader hydrates the process-local picker when schedd reports
	// app-level capacity but this gateway just restarted and missed the
	// original Admit notification. The narrow hook keeps gateway independent
	// of pkg/state.
	liveTargetLoader func(ctx context.Context, appID string) ([]Target, error)
	// liveTargetHydration coalesces cache-reconciliation reads for the same
	// app. A gateway restart can receive a burst before the first request has
	// populated the process-local picker; those requests must share one
	// authoritative database read rather than stampeding Postgres.
	liveTargetHydration singleflight.Group
	// staleTargetRecovery coalesces replacement admissions after a stale
	// target eviction. Without this guard, a burst that discovers the same
	// dead instance could start one replacement per failed request.
	staleTargetRecovery singleflight.Group
	// staleTargets quarantines instances that the forwarding transport has
	// proved unusable. The database row can remain RUNNING until vmmd's
	// liveness report reaches schedd; without this short-lived fence,
	// ReconcileLiveTargets can immediately put the same dead instance back
	// into the picker and recreate a 503 storm.
	staleTargets map[string]time.Time

	// publicAuthCache is the unsealed basic-auth credential
	// cache (issue #477 / ADR-079). nil = no caching; the
	// basic-auth path falls back to per-request unsealing
	// (slower but safe). Production wires it from
	// cmd/gatewayd-internal so the 60s TTL + per-key
	// invalidation through db.NotifyKeyChanged both apply.
	publicAuthCache *PublicAuthCache
	// responseCache (ADR-122 §Decision) is the in-process
	// kind=cache response cache consulted by the Handler on
	// every request. PGBackend holds a pointer so the cmd-side
	// invalidator can drop per-app entries on NotifyAppChanged
	// (a deploy) and wholesale entries on NotifyEdgeRuleChanged
	// (a cache rule creation / mutation / delete). nil-safe: an
	// unwired cache short-circuits the invalidation arms so the
	// notify subscriber doesn't need a wiring branch.
	responseCache *ResponseCache
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

	// scopeMu (issue #272 / ADR-095 PR-B) guards scopeAudit. The
	// map records the most recent scope string the gateway passed
	// to Backend.Admit for each appID, so the unit test
	// (TestPGBackend_AdmitScope) can assert which scope the call
	// carried. Production code never reads this; the schedd
	// client receives scope via the gRPC request once the proto
	// widens (PR-B step 5).
	scopeMu    sync.Mutex
	scopeAudit map[string]string // appID -> last scope arg

	// mirrorMu + mirrorRules (issue #72 / ADR-125 PR-A3) cache
	// the per-app set of enabled mirror rules. RefreshMirrorRules
	// loads from the store, filters to Enabled=true, and swaps
	// the slice under mirrorMu.Lock. LookupMirrorRules is
	// cache-only — no store hop on the customer hot path. The
	// mirror goroutine (PR-A3 commit 3) reads this slice to
	// decide whether to fan out. The map is bounded at the
	// tenant fleet scale (one entry per app that has at least
	// one enabled rule); a rule-less app's slice is an empty
	// non-nil slice (the cache-miss signal) that the lookup
	// returns as (nil, false). PR-A3 does NOT add eviction —
	// Limits.MirrorTargetsPerApp ≤ 3 per app keeps the working
	// set trivial; an app that deletes its last rule has its
	// cache entry overwritten to the empty slice on the next
	// pg_notify refresh.
	mirrorMu    sync.RWMutex
	mirrorRules map[string][]MirrorRuleRow

	// mirrorStore (issue #72 / ADR-125 PR-A3) is the gateway-side
	// seam the RefreshMirrorRules notify handler reads. nil =
	// no refresh; LookupMirrorRules stays in cache-miss mode
	// for every app until a rule is poked (test seam; production
	// wires this from cmd/gatewayd-internal).
	mirrorStore mirrorRulesStore
}

// recordScope (issue #272 / ADR-095 PR-B) is the test seam that
// captures which scope string Admit was last called with for a
// given appID. The unit test asserts the value after exercising
// the prod/preview Lookup-then-Admit sequence to prove scope is
// threaded through to the schedd boundary.
func (b *PGBackend) recordScope(appID, scope string) {
	if b == nil {
		return
	}
	b.scopeMu.Lock()
	defer b.scopeMu.Unlock()
	if b.scopeAudit == nil {
		b.scopeAudit = make(map[string]string)
	}
	b.scopeAudit[appID] = scope
}

// LastScope (issue #272 / ADR-095 PR-B) returns the most recent
// scope the backend recorded for appID. Test-only; production
// code never calls this. Returns ("", false) when no Admit has
// run yet for the appID.
func (b *PGBackend) LastScope(appID string) (string, bool) {
	if b == nil {
		return "", false
	}
	b.scopeMu.Lock()
	defer b.scopeMu.Unlock()
	s, ok := b.scopeAudit[appID]
	return s, ok
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

// WithLiveTargetLoader installs the restart-reconciliation hook used when this
// gateway's process-local picker is empty. A running instance may already
// exist in Postgres even though this process missed its admission or running
// notification; production filters the loader to current live deployments.
func (b *PGBackend) WithLiveTargetLoader(fn func(context.Context, string) ([]Target, error)) *PGBackend {
	if b != nil {
		b.liveTargetLoader = fn
	}
	return b
}

// ReconcileLiveTargets hydrates the process-local picker from the authoritative
// instance state when this gateway has no routable target for appID.
//
// The picker is intentionally a hot-path cache, but an empty cache is not
// proof that an app is cold: an instance may have been admitted by another
// gateway process, by cron/floor, or before this process restarted. The old
// implementation only attempted this hydration after schedd returned
// at-capacity, which was too late because the empty-cache path had already
// started another VM.
//
// The replacement is conditional on the cache still being empty after the
// database read. That preserves a target admitted concurrently while the read
// was in flight and makes the reconciliation safe with the normal Admit path.
func (b *PGBackend) ReconcileLiveTargets(ctx context.Context, appID string) error {
	if b == nil || appID == "" || b.liveTargetLoader == nil {
		return nil
	}
	if b.HealthyCount(appID) > 0 {
		return nil
	}
	_, err, _ := b.liveTargetHydration.Do(appID, func() (any, error) {
		if b.HealthyCount(appID) > 0 {
			return nil, nil
		}
		targets, err := b.liveTargetLoader(ctx, appID)
		if err != nil {
			return nil, fmt.Errorf("gateway: reconcile live targets for app %s: %w", appID, err)
		}
		if len(targets) == 0 {
			return nil, nil
		}

		b.tgtMu.Lock()
		defer b.tgtMu.Unlock()
		// An Admit or RecordTarget may have completed while the loader was
		// querying Postgres. Do not overwrite that newer local observation.
		if b.targetCountLocked(appID) > 0 {
			return nil, nil
		}
		for _, target := range targets {
			b.recordTargetLocked(appID, target)
		}
		return nil, nil
	})
	return err
}

// EnsureWarm ensures one running instance for an actually cold production
// app. It uses schedd's Engine.EnsureWake coordinator, which is shared by all
// wake producers and runs the leader lifecycle on a detached bounded context.
// Deployment-scoped preview wakes continue through Admit because the legacy
// EnsureWake RPC does not yet carry a scope/deployment selector.
//
// This method is deliberately an additive optional backend capability so the
// existing Backend interface and its test doubles remain stable.
func (b *PGBackend) EnsureWarm(ctx context.Context, appID, scope, trigger string) (string, WakeMethod, bool, error) {
	if b == nil || scope != "" {
		return "", WakeMethodUnspecified, false, errors.New("gateway: EnsureWarm requires production scope")
	}
	sched, err := b.resolveSched(ctx, appID)
	if err != nil {
		return "", WakeMethodUnspecified, false, err
	}
	instanceID, nodeID, deploymentID, wakeID, rawMethod, port, err := sched.EnsureWake(ctx, appID, trigger)
	if err != nil {
		return "", WakeMethodUnspecified, false, err
	}
	if instanceID == "" || nodeID == "" {
		return "", WakeMethodUnspecified, true, nil
	}
	b.RecordTarget(appID, Target{
		NodeID:       nodeID,
		InstanceID:   instanceID,
		WakeID:       wakeID,
		Port:         port,
		DeploymentID: deploymentID,
	})
	return wakeID, scheddWakeMethodToGateway(rawMethod), false, nil
}

// WithWarmHint attaches the sticky-warm affinity source for the picker.
// nil is tolerated (the picker remains global round-robin).
// cmd/gatewayd-internal/wires this from the WarmHint stream that schedd exposes
// via the gRPC surface; tests pass a closure that reads from a fake
// or a fixed map.
//
// As of PR #429 the WarmHint stream gRPC RPC is not yet wired, so
// production gateways leave this unset. The picker correctly returns
// "no hint" and continues global request rotation. Sticky-warm remains an
// admission-placement concern (follow-up stream work is tracked in
// docs/adr/025 — see plan file).
func (b *PGBackend) WithWarmHint(fn WarmHintFunc) *PGBackend {
	b.warmHint = fn
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
		router:       router,
		sched:        sched,
		log:          log,
		routes:       NewRouteCache(RouteCacheCap),
		apps:         map[string]App{},
		appsPicker:   map[string]*appPicker{},
		mirrorRules:  map[string][]MirrorRuleRow{},
		staleTargets: map[string]time.Time{},
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

// MirrorRuleRow (issue #72 / ADR-125 PR-A3) is the gateway-side
// projection of a state.MirrorRule. The mirror dispatch goroutine
// (commit 3) needs every field of the rule at run-time — source
// deployment, mirror deployment, sampling percent, body-capture
// flag, customer redact list — so the projection is 1:1 with the
// state type modulo CreatedAt / UpdatedAt (diagnostics-only,
// unread on the hot path). cmd/gatewayd-internal/backend.go does
// the translation from state.MirrorRule to this shape; the gateway
// package never imports pkg/state.
//
// Field semantics live on state.MirrorRule (pkg/state/types.go);
// this is the wire-shape the gateway cares about.
type MirrorRuleRow struct {
	ID                 string
	AccountID          string
	AppID              string
	SourceDeploymentID string
	MirrorDeploymentID string
	Percent            int
	Enabled            bool
	IncludeBody        bool
	RedactHeaders      []string
}

// mirrorRulesStore is the gateway-side seam used by
// RefreshMirrorRules (issue #72 / ADR-125 PR-A3). Returns every
// rule for appID (enabled or not); RefreshMirrorRules filters to
// Enabled=true before caching so a customer who PATCHes a rule
// disabled = true drops out of the picker on the next pg_notify
// refresh. The production adapter wires state.PgStore.ListMirrorRules;
// tests inject a fake that returns canned rows. Mirrors the
// deploymentWeightsStore shape so the cmd-side wire is symmetric.
type mirrorRulesStore interface {
	ListMirrorRules(ctx context.Context, appID string) ([]MirrorRuleRow, error)
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
	// Lookup is on every request. Use the read-mostly cache operation so
	// concurrent hits do not serialize behind LRU promotion; route changes
	// still invalidate the cache through the existing notifier path.
	if appID, ok := b.routes.Peek(host); ok {
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
// Sticky-warm affinity (ADR-025) applies at scheduler admission time. The
// targetSet request picker intentionally rotates across every routable
// instance so a multi-node burst is not pinned to the warm node.
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

// PickWarm is the production-only warm-path probe used by Handler. It keeps
// the fast path explicit at the seam: a routable target is sufficient evidence
// to skip HealthyCount and the wake gate, while an empty picker falls back to
// the existing ensureCapacity path. The method delegates to Pick so there is
// only one picker implementation and no divergence in deployment weighting or
// stale-target handling.
func (b *PGBackend) PickWarm(appID string) PickResult {
	return b.Pick(appID)
}

// HealthyCount returns the number of routable Targets currently
// cached for appID across all live deployments (PR-B /
// issue #556). Drives the WakeGate's shouldWake predicate: stop
// admitting once we're at the plan's effective max_concurrency.
// Σ over deployments — splitting traffic across N deployments
// does NOT double-bill the cap (issue #556 acceptance #3, §6.2-2).
func (b *PGBackend) HealthyCount(appID string) int {
	b.tgtMu.RLock()
	n := b.targetCountLocked(appID)
	b.tgtMu.RUnlock()
	return n
}

func (b *PGBackend) targetCountLocked(appID string) int {
	picker := b.appsPicker[appID]
	if picker == nil {
		return 0
	}
	n := 0
	for _, set := range picker.sets {
		n += len(set.entries)
	}
	return n
}

// RecordTarget adds a target returned by an out-of-band wake path to the
// gateway picker. The normal HTTP admission path calls Admit, which records
// its own target; synth/cron wakes call schedd directly and must publish the
// same routing handle or the next public request will see a running VM as
// unavailable. Re-recording an instance is idempotent.
func (b *PGBackend) RecordTarget(appID string, target Target) {
	if b == nil || appID == "" || target.InstanceID == "" || target.NodeID == "" {
		return
	}
	if target.AddedAt.IsZero() {
		target.AddedAt = time.Now()
	}
	b.tgtMu.Lock()
	defer b.tgtMu.Unlock()
	b.recordTargetLocked(appID, target)
}

func (b *PGBackend) recordTargetLocked(appID string, target Target) {
	b.purgeStaleTargetsLocked(time.Now())
	if until, ok := b.staleTargets[staleTargetKey(appID, target.InstanceID)]; ok && until.After(time.Now()) {
		return
	}
	picker := b.appsPicker[appID]
	if picker == nil {
		picker = &appPicker{sets: map[string]*targetSet{}}
		b.appsPicker[appID] = picker
	}
	bucket := target.DeploymentID
	if bucket == "" {
		bucket = "_legacy"
		target.DeploymentID = bucket
	}
	set := picker.sets[bucket]
	if set == nil {
		set = &targetSet{subCursors: map[string]*atomic.Uint64{}}
		picker.sets[bucket] = set
	}
	if len(picker.weights) == 0 {
		picker.weights = []deploymentWeight{{DeploymentID: bucket, Percent: 100}}
		picker.cum = []int{100}
	}
	set.add(target)
}

const staleTargetQuarantine = 30 * time.Second

func staleTargetKey(appID, instanceID string) string {
	return appID + "\x00" + instanceID
}

// purgeStaleTargetsLocked bounds the quarantine map without adding work to
// Pick. It runs only on cache mutation/reconciliation paths, which are already
// uncommon compared with request routing.
func (b *PGBackend) purgeStaleTargetsLocked(now time.Time) {
	for key, until := range b.staleTargets {
		if !until.After(now) {
			delete(b.staleTargets, key)
		}
	}
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
// scope (issue #272 / ADR-095 PR-B): the per-lookup scope label
// forwarded to schedd. Empty = prod (legacy). Non-empty = preview
// (e.g. "pr-42") — schedd uses it to resolve the preview app's
// live deployment row (LiveDeploymentForScope) and load the
// scope's env vars. The pre-PR-B schedd client signature does
// not carry scope; this method records it on the request shape
// for the test spy (PGBackend_AdmitScope) and forwards it
// through the full chain once the proto + schedd engine
// signature widen (PR-B step 5).
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
// trigger (ADR-127): wake-boot trigger enum value forwarded to
// schedd's AdmitInstance RPC and stamped on the emitted
// wake.boot_started / wake.boot_completed events. The gateway
// always passes "gateway"; future callers (synth handler, replay
// worker) can pass a distinct closed-enum value.
const admissionLifecycleTimeout = 40 * time.Second

type admitResult struct {
	wakeID     string
	method     WakeMethod
	atCapacity bool
	err        error
}

// Admit keeps the request-facing wait cancellable while the underlying
// scheduler admission continues on a bounded lifecycle context. A client
// timeout must not cancel a VM restore after schedd has reserved it; the
// synchronous worker still publishes the target for the next request.
func (b *PGBackend) Admit(ctx context.Context, appID, deploymentID, scope, trigger string, maxConcurrency int) (string, WakeMethod, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", WakeMethodUnspecified, false, err
	}
	lifecycleCtx, lifecycleCancel := context.WithTimeout(context.WithoutCancel(ctx), admissionLifecycleTimeout)
	resultCh := make(chan admitResult, 1)
	go func() {
		defer lifecycleCancel()
		wakeID, method, atCapacity, err := b.admitSynchronous(lifecycleCtx, appID, deploymentID, scope, trigger, maxConcurrency)
		resultCh <- admitResult{wakeID: wakeID, method: method, atCapacity: atCapacity, err: err}
	}()
	select {
	case result := <-resultCh:
		return result.wakeID, result.method, result.atCapacity, result.err
	case <-ctx.Done():
		return "", WakeMethodUnspecified, false, ctx.Err()
	}
}

func (b *PGBackend) admitSynchronous(ctx context.Context, appID, deploymentID, scope, trigger string, maxConcurrency int) (string, WakeMethod, bool, error) {
	// Test seam (issue #272 / ADR-095 PR-B): the per-call scope arg
	// is recorded on the backend so the unit test (TestPGBackend_AdmitScope)
	// can assert which scope each admit carried. Production code
	// reads b.lastScope via the gateway's test infrastructure; the
	// field is intentionally exported-set via method so tests under
	// different packages can read it without a public field.
	b.recordScope(appID, scope)
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
	instanceID, nodeID, returnedDeploymentID, wakeID, rawMethod, atCapacity, port, err := sched.AdmitInstance(ctx, appID, deploymentID, scope, trigger)
	// NOTE: ADR-098's `EnsureWake(ctx, appID)` is the new single-flight
	// hot-path primitive on the gateway's Wake flow (pkg/gateway/pgbackend.go
	// Wake method, issue #854 / PR #854 / 93059ff4). EnsureWake does NOT yet
	// take a scope — its scope support is a future ADR-098 follow-up. PR-B
	// (issue #272 / ADR-095) keeps AdmitInstance's 4-arg signature so the
	// preview path can route to the preview ledger. Both interfaces stay
	// additive per ADR-016.
	if err != nil {
		return "", WakeMethodUnspecified, false, err
	}
	return b.recordAdmission(ctx, appID, deploymentID, instanceID, nodeID, returnedDeploymentID, wakeID, rawMethod, atCapacity, port)
}

// recordAdmission applies one successful schedd result to the gateway's
// target picker. Keeping this separate from the RPC wrapper lets the bounded
// burst path reuse the exact same cache and deployment-bucket semantics as a
// normal Admit call.
func (b *PGBackend) recordAdmission(ctx context.Context, appID, deploymentID, instanceID, nodeID, returnedDeploymentID, wakeID string, rawMethod int32, atCapacity bool, port int) (string, WakeMethod, bool, error) {
	// Use the deploymentID schedd actually used (matches the
	// bucket the picker will route to); fall through to the
	// caller's hint if schedd returned "".
	if returnedDeploymentID != "" {
		deploymentID = returnedDeploymentID
	}
	method := scheddWakeMethodToGateway(rawMethod)
	// AdmitInstance carries atCapacity as a dedicated field
	// (issue #168). Empty nodeID/instanceID are the legacy
	// "no live deployment" signal — kept for parity with
	// the EnsureWake path main migrates the Wake flow onto
	// (ADR-098); AdmitInstance's typed at-capacity is the
	// primary signal here.
	if atCapacity || nodeID == "" || instanceID == "" {
		if atCapacity && b.liveTargetLoader != nil {
			targets, loadErr := b.liveTargetLoader(ctx, appID)
			if loadErr != nil {
				b.log.Warn("gateway: live target hydration failed", "app_id", appID, "err", loadErr)
			} else {
				for _, target := range targets {
					b.RecordTarget(appID, target)
				}
			}
		}
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
	picker := b.appsPicker[appID]
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
	if b.staleTargets == nil {
		b.staleTargets = make(map[string]time.Time)
	}
	now := time.Now()
	b.purgeStaleTargetsLocked(now)
	b.staleTargets[staleTargetKey(appID, instanceID)] = now.Add(staleTargetQuarantine)
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

// RecoverStaleTarget asynchronously restores one serving slot after the
// handler evicts a target that the forwarder proved stale. It is deliberately
// detached from the request context: the failed request must not cancel the
// replacement admission, and a bounded lifecycle context prevents a broken
// schedd from leaving a goroutine behind indefinitely.
func (b *PGBackend) RecoverStaleTarget(ctx context.Context, appID, scope string, maxConcurrency int) {
	if b == nil || appID == "" || maxConcurrency <= 0 || b.HealthyCount(appID) > 0 {
		return
	}
	go func() {
		_, err, _ := b.staleTargetRecovery.Do(appID, func() (any, error) {
			if b.HealthyCount(appID) > 0 {
				return nil, nil
			}
			lifecycleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), admissionLifecycleTimeout)
			defer cancel()
			_, _, atCapacity, err := b.admitSynchronous(lifecycleCtx, appID, "", scope, "stale_target_recovery", maxConcurrency)
			if err != nil {
				return nil, err
			}
			if atCapacity {
				// Another producer may have admitted the replacement while
				// the local cache was empty. Reconcile so this process does
				// not immediately send another request into a false cold
				// path.
				if reconcileErr := b.ReconcileLiveTargets(lifecycleCtx, appID); reconcileErr != nil {
					return nil, reconcileErr
				}
			}
			return nil, nil
		})
		if err != nil && b.log != nil {
			b.log.Warn("gateway: stale target recovery failed", "app_id", appID, "err", err)
		}
	}()
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

// RefreshMirrorRules (issue #72 / ADR-125 PR-A3) reloads the
// per-app mirror-rule cache from the store. The notify loop
// calls this on db.NotifyDeploymentChanged when payload.kind ==
// "mirror" (the apid CRUD handlers stamp the discriminator — see
// cmd/apid/handlers_mirror.go + the kind="mirror" arm in
// cmd/gatewayd-internal/backend.go).
//
// Behaviour:
//
//   - Reads ListMirrorRules(appID). The store returns every rule
//     (enabled or not); the gateway filters to Enabled=true
//     because the dispatch goroutine (commit 3) only ever
//     fires for enabled rules.
//   - Swaps the slice under mirrorMu.Lock; the previous slice is
//     garbage once no goroutine holds a reference. The map entry
//     is created on the first refresh for an app; subsequent
//     refreshes overwrite in place.
//   - Empty slice ≠ cache miss: a rule-less app still gets a
//     map entry pointing at an empty non-nil slice, so the
//     handler's LookupMirrorRules returns ([]MirrorRuleRow{},
//     true) — the dispatch goroutine sees "no mirror" without
//     paying for a second cache check.
//
// Returns the error wrapped with operation context. The notify
// handler logs-and-continues on a non-nil error — a brief
// staleness window is preferable to crashing the edge.
func (b *PGBackend) RefreshMirrorRules(ctx context.Context, appID string) error {
	if b.mirrorStore == nil {
		// No store wired — nothing to refresh. The cache
		// stays empty (all lookups return cache-miss). This
		// is the test seam: tests that don't care about the
		// refresh path leave WithMirrorStore unwired.
		return nil
	}
	rows, err := b.mirrorStore.ListMirrorRules(ctx, appID)
	if err != nil {
		return fmt.Errorf("gatewayd-internal: refresh mirror rules app=%s: %w", appID, err)
	}
	enabled := make([]MirrorRuleRow, 0, len(rows))
	for _, r := range rows {
		if r.Enabled {
			enabled = append(enabled, r)
		}
	}
	b.mirrorMu.Lock()
	b.mirrorRules[appID] = enabled
	b.mirrorMu.Unlock()
	return nil
}

// LookupMirrorRules (issue #72 / ADR-125 PR-A3) is the cache
// reader the handler calls post-Pick to decide whether to fan out
// to a mirror VM. Returns (rules, true) on cache hit — including
// the empty-slice case (a rule-less app still returns true so the
// handler skips the dispatch loop without a second check).
//
// Cache miss returns (nil, false); the handler treats this as
// "no mirror" (mirror never blocks the customer response — the
// next pg_notify for kind="mirror" will populate the cache).
//
// Hot-path discipline: NO store call. RefreshMirrorRules is the
// only path that reads the store, and it runs from the notify
// subscriber on a separate goroutine. A customer who POSTs a
// mirror rule sees the first fan-out fire ~50ms after the apid
// kind="mirror" notify propagates.
func (b *PGBackend) LookupMirrorRules(ctx context.Context, appID string) ([]MirrorRuleRow, bool) {
	b.mirrorMu.RLock()
	rules, ok := b.mirrorRules[appID]
	b.mirrorMu.RUnlock()
	if ok {
		return rules, true
	}
	return nil, false
}

// ScheduleMirror (issue #72 / ADR-124 / ADR-125 PR-A3) is the
// Backend-side implementation of the mirror goroutine's "wake up
// the mirror VM" primitive. Forwards to the per-app schedd client
// (resolved via resolveSched, same path as Admit) so multi-box
// deployments route the mirror wake to the owning node.
//
// On cap-at-max the underlying Scheduler returns an error wrapping
// sched.ErrMirrorSlotAtCapacity; the dispatch goroutine detects
// that and writes a ledger entry with status_diff=true +
// gateway_mirror_dispatched_total{result="cap_at_max"}. We do NOT
// unwrap/re-wrap here — the goroutine inspects errors.Is directly.
func (b *PGBackend) ScheduleMirror(ctx context.Context, appID, mirrorDeploymentID, mirrorRuleID string) (instanceID, wakeID string, err error) {
	sched, err := b.resolveSched(ctx, appID)
	if err != nil {
		return "", "", err
	}
	return sched.AdmitMirrorInstance(ctx, appID, mirrorDeploymentID, mirrorRuleID)
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

// WithMirrorStore (issue #72 / ADR-125 PR-A3) arms the
// mirror-rule store for RefreshMirrorRules. nil = no store
// wired; RefreshMirrorRules no-ops so the cache stays empty
// (every LookupMirrorRules returns cache-miss until the daemon
// restarts — the test seam). Production wires this from
// cmd/gatewayd-internal so a db.NotifyDeploymentChanged event
// with payload.kind == "mirror" triggers the refresh.
//
// Returns *PGBackend for fluent chaining (same shape as
// every other PGBackend.With*).
func (b *PGBackend) WithMirrorStore(s mirrorRulesStore) *PGBackend {
	b.mirrorStore = s
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

// WithResponseCache (ADR-122 §Decision) arms the in-process
// kind=cache response cache so the cmd-side invalidator can
// drop entries on db.NotifyAppChanged (deploy → drop that app's
// entries) and db.NotifyEdgeRuleChanged (cache rule change →
// drop everything; the cache is per-host keyed and per-rule
// invalidation would require the notification to carry the
// rule's match_host, which it doesn't).
//
// nil = no caching (the handler's applyEdgeRuleCache short-
// circuits on a nil cache, so a misconfigured wiring degrades
// to "no cache" rather than "all requests fail"). Production
// wires gateway.NewResponseCache() in cmd/gatewayd-internal.
//
// Returns *PGBackend for fluent chaining (same shape as
// every other PGBackend.With*).
func (b *PGBackend) WithResponseCache(cache *ResponseCache) *PGBackend {
	b.responseCache = cache
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

// WithCertIssuer (ADR-100 / issue #879) arms the cert-remint
// seam. nil = feature dark; the notify subscriber no-ops so a
// misconfigured rollout can't crash the edge. Production wires
// this from cmd/gatewayd-internal so a tenant_surface_changed
// notification triggers a fresh SAN mint; tests inject a fake
// that records the call. The setter returns *PGBackend for
// fluent chaining (same shape as every other PGBackend.With*).
func (b *PGBackend) WithCertIssuer(issuer CertIssuer) *PGBackend {
	b.certIssuer = issuer
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

// ResetCorsPresets (issue #975 #4 PR-B / ADR-129 D4) drops
// the edge-rule matcher's per-host LRU so the next request
// recompiles and re-fetches the up-to-date preset via
// state.GetCorsPresetByID. The compile path bakes the
// preset's allow_origins / allow_methods / etc. into the
// resolved EdgeRuleCORSResolved slice, so a preset edit
// leaves stale resolved shapes in the LRU. Wholesale flush
// matches ResetEdgeRules semantics; the per-account payload
// is informational (the LRU is per-host keyed, not per-
// account; per-account eviction would require a richer
// key — see backend.go's handleInvalidation case for the
// trade-off discussion). nil-safe.
func (b *PGBackend) ResetCorsPresets(accountID string) {
	if b == nil {
		return
	}
	b.ResetEdgeRules()
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

// ResetApp (ADR-091 amendment / §4.1.2.0) drops a single app from
// the apps LRU so the next Backend.Lookup repopulates from PG with
// the up-to-date column values (most importantly
// apps.maintenance_mode). Called from the cmd-side pg_notify
// listener on db.NotifyAppChanged when the trigger fires only when
// maintenance_mode IS DISTINCT FROM old — see
// migrations/00221_apps_maintenance_mode.sql. The apps LRU has no
// TTL — without this listener the gateway keeps stale
// MaintenanceMode for the lifetime of the cache entry, and the
// first node to see the flip returns 503 forever.
//
// nil-safe: an unwired backend (no apps map) short-circuits so
// the notify subscriber doesn't need a wiring branch. Lock
// discipline mirrors ResetEdgeRules at line 1024: write-lock for
// the delete, RLock for reads elsewhere.
func (b *PGBackend) ResetApp(appID string) {
	if b == nil {
		return
	}
	b.appsMu.Lock()
	delete(b.apps, appID)
	b.appsMu.Unlock()
}

// InvalidateResponseCacheByApp (ADR-122 §Decision) drops every
// cached entry whose key has the given appID. Called from
// cmd/gatewayd-internal on db.NotifyAppChanged when the trigger
// fires on a per-app column flip — most importantly a deploy
// (cmd/apid fires NotifyAppChanged post-deploy at
// cmd/apid/handlers_deployments.go:248; the response cache
// must NOT serve the previous release's body under the new
// release's URL, even though the cache key's DeploymentID
// field is empty in v1).
//
// nil-safe: an unwired cache short-circuits so the notify
// subscriber doesn't need a wiring branch.
func (b *PGBackend) InvalidateResponseCacheByApp(appID string) {
	if b == nil || b.responseCache == nil {
		return
	}
	b.responseCache.InvalidateByApp(appID)
}

// InvalidateResponseCacheAll (ADR-122 §Decision) drops every
// cached entry. Called from cmd/gatewayd-internal on
// db.NotifyEdgeRuleChanged when a kind=cache rule is created /
// updated / deleted (cmd/apid/handlers_edge_rules.go:256/455/511).
// Wholesale — per-rule invalidation would require the
// notification payload to carry the rule's match_host (it
// doesn't), and the cache is advisory so a brief miss-bump is
// strictly preferable to a stale serve against a deleted rule.
//
// nil-safe: same posture as InvalidateResponseCacheByApp.
func (b *PGBackend) InvalidateResponseCacheAll() {
	if b == nil || b.responseCache == nil {
		return
	}
	b.responseCache.InvalidateAll()
}

// RequestCertForSurface (ADR-100 / issue #879) delegates to the
// configured CertIssuer so a tenant_surface_changed pg_notify
// re-mints the SAN-aggregated cert for the affected surface. nil-
// safe: an unwired backend (PR-A's default until the issuer is
// set on the production wiring) no-ops so a notify burst before
// the cert engine lands doesn't crash the edge. Errors flow
// through unchanged; the caller (cmd/gatewayd-internal handleInvalidation)
// logs-and-swallows so a transient CA failure can't block the
// notify loop.
func (b *PGBackend) RequestCertForSurface(ctx context.Context, surfaceID string) error {
	if b == nil || b.certIssuer == nil {
		return nil
	}
	return b.certIssuer.RequestCertForSurface(ctx, surfaceID)
}

// resolveSched picks the schedd client that should service appID
// (Phase 2 / Gate A). Returns the per-node client when both hooks
// are configured AND the app has a non-empty NodeID; otherwise
// returns a definitive error.
//
// Multi-host safety cluster PR-7 (audit F5): the legacy single-
// box fallback path is REMOVED. The previous implementation
// returned b.sched (the single-box field) on three transient
// triggers — resolver miss, empty NodeID, clientForApp miss.
// In a multi-box fleet that fallback would route a foreign-owned
// app through the wrong schedd and surface a FailedPrecondition
// storm. The current implementation always returns an error so
// the gateway surfaces a 503 with a useful message rather than a
// silent FailedPrecondition. The legacySingleBox gate that
// previously toggled the fallback was removed alongside the
// fallback itself (no callers remain after PR-7).
func (b *PGBackend) resolveSched(ctx context.Context, appID string) (Scheduler, error) {
	if b.appResolver != nil && b.clientForApp != nil {
		app, ok, err := b.appResolver(ctx, appID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("gatewayd-internal: app %s: not found (transient resolver miss; legacy single-box fallback removed in PR-7)", appID)
		}
		if app.NodeID == "" {
			return nil, fmt.Errorf("gatewayd-internal: app %s has empty NodeID (pre-migration row; legacy single-box fallback removed in PR-7)", appID)
		}
		cli, ok, err := b.clientForApp(ctx, app)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("gatewayd-internal: app %s (node %s): client resolver declined (transient miss; legacy single-box fallback removed in PR-7)", appID, app.NodeID)
		}
		return cli, nil
	}
	return b.sched, nil
}
