// adr: 1741 — schedd's owner cache: what a production wake reads about an
// app, as of one runtime generation.
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// ownerEntry is what a production wake reads about an app before it admits
// an instance, read after the app's runtime generation. Any change to these
// rows moves the generation in the writer's transaction, and the admission
// insert requires it unchanged, so a stale entry is refused, never used.
type ownerEntry struct {
	generation int64
	app        state.App // the apps row; the standards check passed for it
	acct       state.Account
	limits     api.Limits
	dep        state.Deployment // the production selection
	resolved   state.App        // app resolved for dep
	scaling    state.RuntimeScalingState
	// valueReads are the runtime values' three reads (config boundary,
	// values, secret intent); each wake builds its own values from them.
	valueReads runtimeDeploymentInputs
	// An entry exists only for an app whose database cutover barrier was
	// clear at its generation; raising the barrier moves the generation.
}

// ownerCache holds one entry per app this schedd woke, dropped when the app's
// generation moves (app_runtime_changed) and all at once on a resync.
type ownerCache struct {
	mu      sync.Mutex
	entries map[string]ownerEntry
}

func (c *ownerCache) lookup(appID string) (ownerEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[appID]
	return entry, ok
}

// store keeps entry unless a newer generation of the app is already held.
func (c *ownerCache) store(entry ownerEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]ownerEntry{}
	}
	if held, ok := c.entries[entry.app.ID]; ok && held.generation > entry.generation {
		return
	}
	c.entries[entry.app.ID] = entry
}

// invalidate drops the app's entry unless it is already at generation or
// newer. Generation 0 (a deleted app) always drops it.
func (c *ownerCache) invalidate(appID string, generation int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	held, ok := c.entries[appID]
	if !ok || (generation > 0 && held.generation >= generation) {
		return false
	}
	delete(c.entries, appID)
	return true
}

func (c *ownerCache) invalidateAll() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.entries)
	c.entries = nil
	return n
}

// WithOwnerCache turns on schedd's owner cache (ADR-1741, schedd
// `owner_cache`, off by default). The caller feeds it app_runtime_changed
// notifications and resyncs through HandleOwnerCacheNotification.
func (e *Engine) WithOwnerCache() *Engine {
	if e == nil {
		return e
	}
	e.ownerCacheEnabled.Store(true)
	return e
}

// Outcomes of a boot's publication on its admission's generation.
const (
	ownerCacheGuardedPublish      = "guarded_publish"
	ownerCacheGuardedPublishMoved = "guarded_publish_moved"
)

func (e *Engine) observeOwnerCache(outcome string) {
	if e != nil && e.ops != nil {
		e.ops.OwnerCacheTotal(outcome).Inc()
	}
}

// HandleOwnerCacheNotification applies one notification from
// db.SubscribeWithResync on app_runtime_changed: a generation drops the
// app's entry, and a resync, which means notifications may have been lost,
// drops every entry.
func (e *Engine) HandleOwnerCacheNotification(n db.Notification) {
	if n.Channel == db.ResyncChannel {
		e.owners.invalidateAll()
		e.observeOwnerCache("resync")
		return
	}
	var payload struct {
		AppID      string `json:"app_id"`
		Generation int64  `json:"generation"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil || payload.AppID == "" {
		e.owners.invalidateAll()
		e.observeOwnerCache("resync")
		return
	}
	if e.owners.invalidate(payload.AppID, payload.Generation) {
		e.observeOwnerCache("invalidated")
	}
}

var errNoOwnerEntry = errors.New("sched: app has no owner cache entry")

// gateGuardedByGeneration reports whether a production wake may take the
// workload-retirement gate's verdict from a thaw ticket or an owner cache
// entry instead of reading it first: both were issued only while the gate
// passed, and a retirement moves the generation their guarded writes check.
// A coordinated wake's selection is production's too (ADR-1740 amendment
// 2026-10-21); a step that decides from fresh reads runs the gate first.
func (e *Engine) gateGuardedByGeneration(ctx context.Context, appID, deploymentID string) bool {
	if !e.frozenFastPath.Load() && !e.ownerCacheEnabled.Load() {
		return false
	}
	return deploymentID == "" && ScopeFrom(ctx) == "" && ctx.Value(ownerCacheBypassKey{}) == nil
}

type deferredWorkloadGateKey struct{}

// deferredWorkloadGate is a wake's workload-retirement gate held back for a
// thaw ticket or an owner cache entry. Whichever step of the wake decides
// from fresh reads first runs it; checked records that it ran.
type deferredWorkloadGate struct {
	scope   string
	checked atomic.Bool
}

func withDeferredWorkloadGate(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, deferredWorkloadGateKey{}, &deferredWorkloadGate{scope: scope})
}

// checkDeferredWorkloadGate runs ctx's deferred gate once. A wake without
// one ran its gate before phase 1.
func (e *Engine) checkDeferredWorkloadGate(ctx context.Context, appID string) error {
	gate, ok := ctx.Value(deferredWorkloadGateKey{}).(*deferredWorkloadGate)
	if !ok || gate.checked.Load() {
		return nil
	}
	if err := state.CheckEnvironmentWorkloadAdmission(ctx, e.store, appID, gate.scope); err != nil {
		return err
	}
	gate.checked.Store(true)
	return nil
}

// ownerGenerationHolds rereads the generation an admission took from the
// owner cache. ADR-1151's thaw and the warm-pool promotion publish without
// the admission insert's guard, so they check it beside their own reads,
// before vmmd resumes anything; a moved generation refuses with ErrConflict
// and the admission retries from fresh reads. Without the cache it reads
// nothing.
func (e *Engine) ownerGenerationHolds(ctx context.Context) error {
	generations, ok := e.store.(state.AppRuntimeGenerationReader)
	if !ok {
		return nil
	}
	return state.CheckRuntimeGenerationGuard(ctx, generations)
}

// ownerEntryForPhaseOne returns the app's entry for wake phase 1 under the
// same conditions as admission.
func (e *Engine) ownerEntryForPhaseOne(ctx context.Context, appID, deploymentID string) (ownerEntry, bool) {
	return e.ownerEntryForAdmission(ctx, appID, deploymentID)
}

// runningInstanceOfDeployment is runningInstanceForWake for a deployment the
// caller already resolved: a running instance of it, or ErrNotFound.
func (e *Engine) runningInstanceOfDeployment(ctx context.Context, appID string, dep state.Deployment) (state.Instance, error) {
	if dep.AppID != appID || dep.Status != state.DeployLive {
		return state.Instance{}, state.ErrNotFound
	}
	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return state.Instance{}, err
	}
	for _, instance := range instances {
		if instance.DeploymentID == dep.ID && instance.State == string(state.StateRunning) {
			return instance, nil
		}
	}
	return state.Instance{}, state.ErrNotFound
}

type ownerCacheBypassKey struct{}

type ownerCacheUseKey struct{}

// withOwnerCacheUse records in used whether an admission in ctx decided
// from the owner cache.
func withOwnerCacheUse(ctx context.Context, used *bool) context.Context {
	return context.WithValue(ctx, ownerCacheUseKey{}, used)
}

func markOwnerCacheUse(ctx context.Context) {
	if used, ok := ctx.Value(ownerCacheUseKey{}).(*bool); ok {
		*used = true
	}
}

// withoutOwnerCache makes admissions in ctx read every row afresh, including
// the app a wake's phase 1 may have handed on.
func withoutOwnerCache(ctx context.Context) context.Context {
	return context.WithValue(forgetWakeApp(ctx), ownerCacheBypassKey{}, true)
}

// ownerEntryForAdmission returns the app's entry for an ordinary production
// admission: no explicit deployment, no scope, cache on and not bypassed.
// A coordinated wake (EnsureWake, the gateway's path) is served only when
// its selection is the entry's production deployment, environment and
// account (ADR-1741 amendment 2026-10-21). A failed load falls back to the
// ordinary reads, which report the error the wake path always reported.
func (e *Engine) ownerEntryForAdmission(ctx context.Context, appID, deploymentID string) (ownerEntry, bool) {
	if deploymentID != "" || ScopeFrom(ctx) != "" || ctx.Value(ownerCacheBypassKey{}) != nil {
		return ownerEntry{}, false
	}
	entry, err := e.ownerEntryFor(ctx, appID)
	if err != nil {
		return ownerEntry{}, false
	}
	if selected, ok := wakeEnvironmentFrom(ctx); ok && !entry.serves(selected) {
		e.observeOwnerCache("selection")
		return ownerEntry{}, false
	}
	return entry, true
}

// serves reports whether a coordinated wake's selection is the one this
// entry holds. The selection was read moments ago; when it differs, the
// entry is stale or the selection is not production's, and the wake decides
// from its own reads.
func (entry ownerEntry) serves(selected wakeEnvironment) bool {
	return selected.app.ID == entry.app.ID && selected.deployment.ID == entry.dep.ID &&
		selected.account.ID == entry.acct.ID && selected.owner.Scope == entry.scaling.Scope &&
		selected.owner.EnvironmentID == entry.scaling.EnvironmentID
}

// ownerEntryFor returns the app's entry, loading it on a miss. It serves
// production wakes only, and only when the cache is on.
func (e *Engine) ownerEntryFor(ctx context.Context, appID string) (ownerEntry, error) {
	if !e.ownerCacheEnabled.Load() {
		return ownerEntry{}, errNoOwnerEntry
	}
	if entry, ok := e.owners.lookup(appID); ok {
		e.observeOwnerCache("hit")
		return entry, nil
	}
	e.observeOwnerCache("miss")
	entry, err := e.loadOwnerEntry(ctx, appID)
	if err != nil {
		return ownerEntry{}, err
	}
	e.owners.store(entry)
	return entry, nil
}

// loadOwnerEntry reads the app's generation first, then everything a
// production wake reads before admission. A change after the generation read
// moves it, and the admission insert then refuses the entry.
func (e *Engine) loadOwnerEntry(ctx context.Context, appID string) (ownerEntry, error) {
	generations, ok := e.store.(state.AppRuntimeGenerationReader)
	if !ok {
		return ownerEntry{}, errNoOwnerEntry
	}
	generation, err := generations.AppRuntimeGeneration(ctx, appID)
	if err != nil {
		return ownerEntry{}, fmt.Errorf("sched: owner cache: generation: %w", err)
	}
	// A production workload being retired gets no entry; its retirements
	// move the generation, so an entry implies the gate passed.
	if err := state.CheckEnvironmentWorkloadAdmission(ctx, e.store, appID, ""); err != nil {
		return ownerEntry{}, err
	}
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return ownerEntry{}, fmt.Errorf("sched: owner cache: app: %w", err)
	}
	// Only a passing standards check is cached; a refusal is re-read so
	// its error stays the ordinary path's.
	if err := e.checkApplicationStandardAdmission(ctx, app); err != nil {
		return ownerEntry{}, err
	}
	acct, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return ownerEntry{}, fmt.Errorf("sched: owner cache: account: %w", err)
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !acct.Active() || !ok {
		return ownerEntry{}, errNoOwnerEntry
	}
	dep, err := state.ResolveProductionDeployment(ctx, e.store, appID)
	if err != nil {
		return ownerEntry{}, err
	}
	resolved, err := state.ResolveAppForDeployment(ctx, e.store, app, dep)
	if err != nil {
		return ownerEntry{}, err
	}
	scaling, err := e.runtimeScalingStateForDeployment(ctx, resolved, dep)
	if err != nil {
		return ownerEntry{}, err
	}
	// A fenced app gets no entry; the ordinary path reports the barrier.
	if err := e.checkManagedPostgresAdmission(ctx, appID); err != nil {
		return ownerEntry{}, err
	}
	valueReads := e.readRuntimeDeploymentInputs(ctx, app.AccountID, dep)
	if _, err := buildRuntimeDeploymentValues(resolved, dep, valueReads, false); err != nil {
		return ownerEntry{}, err
	}
	return ownerEntry{generation: generation, app: app, acct: acct, limits: limits, dep: dep, resolved: resolved, scaling: scaling,
		valueReads: valueReads}, nil
}
