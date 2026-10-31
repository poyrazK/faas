// recovery_arbiter.go — Workstream B (issue #1184, ADR-137)
// single decision policy for the per-tick recovery sweep.
//
// Why a single arbiter rather than ad-hoc decisions inside
// live_migrator + deadnode_reconciler: the two paths previously
// could both fire on the same instance (live-migrate enqueue +
// reconcile-fail) and the race had no clean winner. Folding both
// into one Decide() function means the per-tick loop has exactly
// one source of truth for the migrate-vs-recreate question.
//
// Per tick:
//
//   - List nodes whose lifecycle is in {draining, unavailable,
//     recovering} (the NodeListRecoverable surface from
//     pkg/state/store.go).
//   - For each, list live instances on the node
//     (InstanceListByNodeForRecovery).
//   - For each instance, call Decide(node, instance) → LiveMigrate
//     or Recreate or None; dispatch through the migration and
//     recreate primitives wired by the schedd bootstrap.
//
// The arbiter is a pure function over (node, instance) — no
// goroutines, no DB access. The tick loop (cmd/schedd main.go)
// owns the goroutine boundary, matching the Heartbeat and
// DeadNodeReconciler precedent.
package sched

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Decision is the per-instance verdict the arbiter hands the
// tick loop. Closed enum — a future Recover vs Defer distinction
// would extend this rather than add a parallel flag.
type Decision int

const (
	// DecisionNone — skip this instance. PARKED rows fall
	// here (the arbiter doesn't touch them; the rebalancer
	// owns parked-row pickup).
	DecisionNone Decision = iota
	// DecisionLiveMigrate — enqueue a live-migration. The
	// instance has a usable snapshot and the destination
	// peer (when picked) has headroom; the LiveMigrator
	// primitive owns the rest.
	DecisionLiveMigrate
	// DecisionRecreate — the instance has no usable
	// snapshot. The Engine.RecreateInstance primitive
	// (Task #60) transitions the row to PARKED with
	// kind='recovery_recreate' and emits the recovery
	// event. The recreation re-uses the snapshot at the
	// next wake.
	DecisionRecreate
	// DecisionPark — move the instance off its own, still healthy
	// node (Engine.ParkForDrain): a RUNNING instance is withdrawn
	// from routing and its in-flight requests drained before it is
	// stopped, a WARM one is destroyed. Its next request wakes it on
	// an admitting node. A graceful drain uses it for WARM rows at
	// once and for RUNNING rows still on the node
	// api.DrainParkAfterSeconds after the drain began
	// (ADR-20261024085817).
	DecisionPark
)

// String is the human-readable form used in the recovery
// dashboard and slog lines. Not part of any contract; for
// wire-level labelling use the kind set on the typed recovery
// events (events.RecoveryEvent.Kind).
func (d Decision) String() string {
	switch d {
	case DecisionLiveMigrate:
		return "live-migrate"
	case DecisionRecreate:
		return "recreate"
	case DecisionPark:
		return "park"
	}
	return "none"
}

// MigrationDispatcher is the minimum surface the arbiter needs
// to dispatch a live-migration verdict. The interface keeps the
// test seam tight and lets the schedd reuse its migration harness.
type MigrationDispatcher interface {
	Enqueue(ctx context.Context, instanceID string) error
}

// MigrationDispatcherFunc adapts an engine method or closure to the
// MigrationDispatcher contract used by the arbiter.
type MigrationDispatcherFunc func(ctx context.Context, instanceID string) error

func (f MigrationDispatcherFunc) Enqueue(ctx context.Context, instanceID string) error {
	return f(ctx, instanceID)
}

// RecreateDispatcher is the minimum surface the arbiter needs
// to dispatch a recreate verdict. Engine.RecreateInstance
// (Task #60) implements this.
type RecreateDispatcher interface {
	RecreateInstance(ctx context.Context, instanceID string) error
}

// ParkDispatcher is the minimum surface the arbiter needs to
// dispatch a park verdict. Engine.ParkForDrain implements it.
type ParkDispatcher interface {
	ParkForDrain(ctx context.Context, instanceID string) error
}

// Arbiter is the pure-function core. The tick loop holds one
// per schedd; nil is tolerated at construction time so the
// cmd/schedd bootstrap can wire the primitives after the
// engine is built.
type Arbiter struct {
	liveMig  MigrationDispatcher
	recreate RecreateDispatcher
	park     ParkDispatcher
	// liveMigConcurrency bounds the live migrations Tick runs at once
	// for one node (api.MigrateLiveConcurrency); parkConcurrency bounds
	// its drain parks (api.DrainParkConcurrency).
	liveMigConcurrency int
	parkConcurrency    int
	// drainMigrationWindow and drainParkAfter time a graceful drain from
	// its drain_initiated_at (api.DrainLiveMigrationWindowSeconds,
	// api.DrainParkAfterSeconds).
	drainMigrationWindow time.Duration
	drainParkAfter       time.Duration
	now                  func() time.Time
}

// NewArbiter wires the two dispatch targets. Either may be
// nil — the corresponding decision becomes a no-op (the
// arbiter still returns the right verdict; dispatch logs a
// warn and moves on).
func NewArbiter(lm MigrationDispatcher, rp RecreateDispatcher) *Arbiter {
	return &Arbiter{
		liveMig: lm, recreate: rp, liveMigConcurrency: api.MigrateLiveConcurrency,
		parkConcurrency:      api.DrainParkConcurrency,
		drainMigrationWindow: time.Duration(api.DrainLiveMigrationWindowSeconds) * time.Second,
		drainParkAfter:       time.Duration(api.DrainParkAfterSeconds) * time.Second,
		now:                  time.Now,
	}
}

// WithParkDispatcher wires the park verdict's dispatch target. Without one
// a park verdict is counted as skipped.
func (a *Arbiter) WithParkDispatcher(p ParkDispatcher) *Arbiter {
	a.park = p
	return a
}

// WithDrainTiming overrides the drain's live-migration window and the
// point after which it parks, and the clock both are read against. Zero
// values keep the current setting.
func (a *Arbiter) WithDrainTiming(window, parkAfter time.Duration, now func() time.Time) *Arbiter {
	if window > 0 {
		a.drainMigrationWindow = window
	}
	if parkAfter > 0 {
		a.drainParkAfter = parkAfter
	}
	if now != nil {
		a.now = now
	}
	return a
}

// drainPhase places a graceful drain on its timeline: before the
// live-migration window closes, between the window and the park point
// (no new handoff may start, and one started inside the window may still
// be in flight), or past the park point. A node without drain_initiated_at
// stays in the migration phase, as every drain did before.
type drainPhase int

const (
	drainMigrating drainPhase = iota
	drainSettling
	drainParking
)

func (a *Arbiter) drainPhaseOf(node state.ComputeNode) drainPhase {
	if node.DrainInitiatedAt == nil {
		return drainMigrating
	}
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	elapsed := now().Sub(*node.DrainInitiatedAt)
	switch {
	case elapsed >= a.drainParkAfter:
		return drainParking
	case elapsed >= a.drainMigrationWindow:
		return drainSettling
	}
	return drainMigrating
}

// WithLiveMigrationConcurrency overrides how many live migrations Tick runs
// at once for one node. Values below 1 keep the current setting.
func (a *Arbiter) WithLiveMigrationConcurrency(n int) *Arbiter {
	if n >= 1 {
		a.liveMigConcurrency = n
	}
	return a
}

// Decide returns the per-instance verdict. Pure function:
// same inputs → same output, no goroutines, no DB access.
// The caller (Tick) owns the dispatch side-effect.
//
// Decision matrix (ADR-137 §"Decision policy"):
//
//	node.Lifecycle    instance.State   → Decision
//	───────────────────────────────────────────────
//	draining          running         → LiveMigrate inside the drain's
//	                                    live-migration window, None
//	                                    while handoffs settle, then Park
//	                  draining        → None, then Park past the park
//	                                    point (resumes a withdrawal)
//	                  warm            → Park (a paused pool VM; the
//	                                    pool refills on admitting nodes)
//	                  frozen          → Recreate (parks it)
//	                  cold_booting|waking → None
//	                                  (a healthy node still owns the boot)
//	                  other           → None
//	recovering        parked           → None
//	recovering        running         → LiveMigrate
//	                  cold_booting|waking → None
//	                                  (a recovered node still owns the boot)
//	                  other           → None
//	unavailable       parked           → None
//	unavailable       running|waking   → LiveMigrate
//	unavailable       cold_booting     → Recreate
//	                                  (cold-boot rows have no
//	                                  usable snapshot to migrate;
//	                                  cleanest path is recreate)
//	*                 failed|terminal  → None
//	                                  (already terminal; nothing
//	                                  to recover)
//
// Running rows on an unavailable or force-draining node start with a
// LiveMigrate verdict so the normal handoff can preserve state when the
// source is still reachable. If that handoff fails for a reason other than a
// peer winning the row, Tick falls back to RecreateInstance. This is the
// failure-safe landing for a source that cannot complete a handoff: the row
// is parked, its reservation is released, and the service reconciler can
// admit a replacement on a healthy node. Healthy draining/recovering nodes
// retain retry semantics because their source VM is still trusted.
func (a *Arbiter) Decide(node state.ComputeNode, instance state.RecoveryInstance) Decision {
	// Job-task instances intentionally have no app/deployment row. They cannot
	// use the app snapshot handoff; their lease and stuck-task reaper own retry.
	if instance.Kind == "job_task" {
		return DecisionNone
	}
	instanceState := strings.ToLower(instance.State)
	switch instanceState {
	case "parked", "failed", "terminated":
		return DecisionNone
	case "migrating", "snapshotting":
		// In-flight guard (ADR-137 follow-up / fix #6): a peer
		// primitive (live_migrator, snapshot_reaper) owns this
		// row. Issuing a fresh verdict from this arbiter on the
		// next tick would race the in-flight transition — a
		// "migrating" row would get a second LiveMigrate enqueue
		// and double-fire the destination's lease CAS. The
		// owning primitive clears the state when it lands; the
		// next tick sees the row in its post-transition state
		// (parked / failed / cold_booting again on a different
		// node) and resumes normal decision-making.
		return DecisionNone
	}
	if instanceState == string(state.StateFrozen) {
		// ADR-1151: a frozen VM is disposable and cannot be live-migrated
		// (vmmd refuses to snapshot it). The recreate dispatch parks it;
		// the deployment's snapshot stays.
		switch node.Lifecycle {
		case state.NodeLifecycleDraining, state.NodeLifecycleForceDraining,
			state.NodeLifecycleRecovering, state.NodeLifecycleUnavailable:
			return DecisionRecreate
		}
		return DecisionNone
	}
	if strings.HasPrefix(instanceState, "evicting") {
		// Eviction sweep (rebalancer is mid-flight removing it)
		// owns this row — recreating here would re-allocate the
		// row the eviction is trying to free.
		return DecisionNone
	}
	switch node.Lifecycle {
	case state.NodeLifecycleDraining:
		return a.decideDraining(node, instanceState)
	case state.NodeLifecycleForceDraining:
		switch instanceState {
		case string(state.StateColdBooting), string(state.StateWaking):
			return DecisionRecreate
		}
		return DecisionLiveMigrate
	case state.NodeLifecycleRecovering:
		switch instanceState {
		case string(state.StateRunning):
			return DecisionLiveMigrate
		}
		return DecisionNone
	case state.NodeLifecycleUnavailable:
		switch instanceState {
		case string(state.StateColdBooting), string(state.StateWaking):
			return DecisionRecreate
		}
		return DecisionLiveMigrate
	}
	// active / empty lifecycle / unknown — out of scope.
	return DecisionNone
}

// decideDraining is Decide for a graceful drain. A handoff that keeps
// failing must not hold the node forever (ADR-20261024085817): after the
// live-migration window and one migration lease, whatever is still RUNNING
// is withdrawn from routing, drained of its requests and stopped. A row
// already withdrawn (a drain park that a failed destroy interrupted, or a
// runtime-config predecessor) is finished the same way.
func (a *Arbiter) decideDraining(node state.ComputeNode, instanceState string) Decision {
	switch instanceState {
	case string(state.StateWarm):
		return DecisionPark
	case string(state.StateDraining):
		if a.drainPhaseOf(node) == drainParking {
			return DecisionPark
		}
		return DecisionNone
	case string(state.StateRunning):
		switch a.drainPhaseOf(node) {
		case drainParking:
			return DecisionPark
		case drainSettling:
			return DecisionNone
		}
		return DecisionLiveMigrate
	}
	return DecisionNone
}

// Tick runs one recovery sweep. The caller (cmd/schedd main.go)
// owns the goroutine + interval; the arbiter owns the
// dispatch policy. Errors are logged via slog (the caller
// passes the logger via the dispatch closures).
//
// Tick returns the count of (live-migrate, recreate, none)
// verdicts so the caller can feed the §12 recovery panel
// without the arbiter itself owning Prometheus primitives.
// A dispatched park counts as a recreate: the row lands PARKED
// and its next request wakes it elsewhere.
func (a *Arbiter) Tick(ctx context.Context, nodes []state.ComputeNode, instancesByNode map[string][]state.RecoveryInstance) (liveMig, recreate, skipped int, err error) {
	for _, node := range nodes {
		if node.Lifecycle != state.NodeLifecycleDraining &&
			node.Lifecycle != state.NodeLifecycleForceDraining &&
			node.Lifecycle != state.NodeLifecycleUnavailable &&
			node.Lifecycle != state.NodeLifecycleRecovering {
			continue
		}
		var jobs []nodeJob
		for _, instance := range instancesByNode[node.ID] {
			switch a.Decide(node, instance) {
			case DecisionNone:
				skipped++
			case DecisionLiveMigrate:
				if a.liveMig == nil {
					liveMig++
					continue
				}
				jobs = append(jobs, nodeJob{id: instance.ID})
			case DecisionPark:
				if a.park == nil {
					skipped++
					continue
				}
				jobs = append(jobs, nodeJob{id: instance.ID, park: true})
			case DecisionRecreate:
				if a.recreate != nil {
					if e := a.recreate.RecreateInstance(ctx, instance.ID); e != nil {
						err = e
						continue
					}
				}
				recreate++
			}
		}
		migrated, fellBack, dispatchErr := a.dispatchNode(ctx, node, jobs)
		liveMig += migrated
		recreate += fellBack
		if dispatchErr != nil {
			err = errors.Join(err, dispatchErr)
		}
	}
	return liveMig, recreate, skipped, err
}

// nodeJob is one live migration, or with park set one park, that Tick
// dispatches for a node.
type nodeJob struct {
	id   string
	park bool
}

// dispatchNode runs one node's live migrations with at most
// liveMigConcurrency in flight, and its drain parks with at most
// parkConcurrency. A handoff is dominated by snapshot transfer (~85 s
// measured in production), so dispatching them one by one made a drain take
// that long per running instance; a drain park waits up to the request
// bound. Recreates stay inline in Tick: they are row updates. A park counts
// in fellBack.
func (a *Arbiter) dispatchNode(ctx context.Context, node state.ComputeNode, jobs []nodeJob) (migrated, fellBack int, err error) {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		errs []error
	)
	migrateSlots := make(chan struct{}, max(a.liveMigConcurrency, 1))
	parkSlots := make(chan struct{}, max(a.parkConcurrency, 1))
	for _, job := range jobs {
		slots := migrateSlots
		if job.park {
			slots = parkSlots
		}
		slots <- struct{}{}
		wg.Add(1)
		go func(job nodeJob, slots chan struct{}) {
			defer func() { <-slots; wg.Done() }()
			var m, f int
			var e error
			if job.park {
				if e = a.park.ParkForDrain(ctx, job.id); e == nil {
					f = 1
				}
			} else {
				m, f, e = a.migrateOne(ctx, node, job.id)
			}
			mu.Lock()
			defer mu.Unlock()
			migrated += m
			fellBack += f
			if e != nil {
				errs = append(errs, e)
			}
		}(job, slots)
	}
	wg.Wait()
	return migrated, fellBack, errors.Join(errs...)
}

// migrateOne dispatches one live migration, falling back to a recreate where
// recoveryMigrationFallback allows it.
func (a *Arbiter) migrateOne(ctx context.Context, node state.ComputeNode, id string) (migrated, fellBack int, err error) {
	e := a.liveMig.Enqueue(ctx, id)
	if e == nil {
		return 1, 0, nil
	}
	if recoveryMigrationFallback(node, e, a.recreate) {
		if fallbackErr := a.recreate.RecreateInstance(ctx, id); fallbackErr != nil {
			return 0, 0, errors.Join(e, fallbackErr)
		}
		return 0, 1, nil
	}
	return 0, 0, e
}

// recoveryMigrationFallback reports whether a failed migration should be
// converted into a recreate attempt. Only unhealthy source lifecycles qualify:
// a failed migration during a normal drain/recovery is retryable and should
// not discard a still-healthy VM. Conflicts and missing rows are benign peer
// races, while canceled contexts must not start new work during shutdown.
func recoveryMigrationFallback(node state.ComputeNode, migrationErr error, recreate RecreateDispatcher) bool {
	if recreate == nil || migrationErr == nil ||
		errors.Is(migrationErr, state.ErrConflict) ||
		errors.Is(migrationErr, state.ErrNotFound) ||
		errors.Is(migrationErr, context.Canceled) ||
		errors.Is(migrationErr, context.DeadlineExceeded) {
		return false
	}
	return node.Lifecycle == state.NodeLifecycleUnavailable ||
		node.Lifecycle == state.NodeLifecycleForceDraining
}
