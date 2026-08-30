// migration_handoff.go — Tier A5 (ADR-066) cross-node live-instance
// handoff orchestrator.
//
// Schedd's per-instance live-migration is a four-phase commit:
//   1. PrepareLiveMigration — the new owner vmmd's schedd dials
//      the DYING vmmd and asks it to pause the running VM +
//      write a fresh snapshot to the canonical storage backend.
//      The dying vmmd mints a lease_token and returns the
//      snapshot storage keys.
//   2. AdoptMigratedInstance — the new owner vmmd's schedd dials
//      the NEW owner vmmd and asks it to restore the snapshot
//      from Phase 1.
//   3. MigrateInstanceOwner — the orchestrator runs the
//      conditional UPDATE on the instances row: state
//      'migrating' → 'running', node_id flips to the new
//      owner, the migration lineage columns are stamped
//      (migrated_from_node_id, migrated_at, lease_token), and
//      apps.migrated_at is stamped in the same transaction.
//   4. AcknowledgeMigration — the new owner vmmd's schedd dials
//      the DYING vmmd and tells it "Phase 3 committed; you can
//      destroy the paused VM and free the netns/jail".
//
// On any failure between Phase 1 and Phase 3, the orchestrator
// runs Phase 4 (CancelLiveMigration) — the dying vmmd resumes
// the paused VM and the snapshot stays where it was. Phase 4 is
// best-effort; the row has already been rolled back via
// Store.CancelInstanceMigration before Phase 4 fires.
//
// A lease clock bounds the whole flow: the dying vmmd mints the
// lease_token at Phase 1 and runs a per-instance lease-expiry
// timer for MigrateLiveLeaseSeconds (pkg/api/limits.go). On
// lease expiry, the dying vmmd resumes the VM regardless of
// whether the orchestrator ran Phase 4. The orchestrator's
// conditional UPDATE at Phase 3 carries the lease_token as part
// of the predicate (well, the Phase-2 MarkInstanceMigrating
// runs first, then Phase 3 is conditional on state='migrating'
// + node_id=fromNodeID) so a stale lease can never silently
// commit.
//
// Concurrency: each candidate instance gets its own goroutine
// (the parent Engine.MigrateLiveInstances spawns one per
// instance up to MigrateLiveMaxPerTick). The four-phase
// orchestrator runs synchronously inside the goroutine so the
// lease clock is observable to the caller.
//
// Failure modes:
//   - Peer race (peer re-owner / peer rollback): the
//     conditional UPDATEs at Phase 2 + Phase 3 return
//     ErrConflict. The orchestrator logs Warn and drops; the
//     metric bumps outcome="conflict".
//   - Lease expiry: MigrateLiveLeaseSeconds elapses before
//     Phase 3 commits. The dying vmmd has already resumed the
//     VM (Phase 4 ran there). The orchestrator's Phase 4 fails
//     on the wire (the VM is already RUNNING), which is logged
//     Debug and dropped. Metric bumps outcome="lease_expired".
//   - Peer failure (Phase 1 / Phase 2 / Phase 3.5 / Phase 4
//     gRPC dial error): logged Warn, Phase 4 fires, metric
//     bumps outcome="peer_failure".
//   - Instance gone (Phase 2 / Phase 3 ErrNotFound): the
//     instance was hard-deleted mid-flight. Logged Warn,
//     dropped, no metric bump (this is a cold-start race, not
//     a migration failure).
//
// Hard limits policy (CLAUDE.md): every limit is a constant in
// pkg/api/limits.go, never inlined here.

package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// MigrationHarness owns the four-phase cross-node live-instance
// handoff. One per Engine; cmd/schedd builds it from
// EngineOpts and passes it down. The handoff's only state is
// the per-orchestrator context (so a fan-out caller can cancel
// the whole batch on ctx.Done) and the metrics accessor (so the
// outcome dispatcher can bump liveMigrationDecisions).
type MigrationHarness struct {
	store   state.Store
	vmm     RoutedVMM
	metrics apiMigrationMetrics
	log     *slog.Logger
	// events is the recovery-timeline fan-out (Workstream B /
	// issue #1184 / Task #66). nil opts out (legacy fixtures).
	events *events.Platform
	// ledger is schedd's per-node NodeLedger. Tier A5 (ADR-066)
	// reserves destination RAM + vCPU at Phase 3 BEFORE the wire
	// call so a flood of inbound migrations cannot over-admit a
	// destination node (invariant §6.2-2). The reservation is
	// released on Phase 3 wire failure; on Phase 4 success the
	// reservation persists as the instance is now RUNNING on the
	// destination (the source's ledger entry is released by the
	// gateway's pg_notify instance_changed listener on
	// state='migrating' — see cmd/gatewayd-internal/backend.go).
	ledger *NodeLedger
	// destinationCeilingMB is the per-node RAM admission ceiling for
	// the destination node (compute_nodes.admission_ceiling_mb for
	// newOwnerNodeID), populated at NewMigrationHarness time from
	// store.ListComputeNodes. Zero falls back to the global
	// api.RAMAdmissionCeilingMB inside NodeLedger.Admit (the legacy
	// single-box posture); on a heterogeneous fleet a non-zero
	// value is load-bearing — without it the Phase 3 reservation
	// would over-admit a destination whose ceiling is smaller than
	// the global ceiling (violating invariant §6.2-2).
	destinationCeilingMB int
	// destinationVCPUBudget is the per-node vCPU admission budget
	// for the destination node (compute_nodes.vcpu_budget for
	// newOwnerNodeID). Zero falls back to api.VCPUSlots inside
	// NodeLedger.Admit. Same load-bearing rationale as
	// destinationCeilingMB.
	destinationVCPUBudget int
	// nodeCeilingResolver resolves a nodeID to its (RAM ceiling,
	// vCPU budget) row from compute_nodes. Wired by the engine so
	// the harness doesn't import pkg/state directly (one-way
	// import graph: pkg/state → pkg/sched is forbidden, the
	// engine wires both). nil means "no resolver wired" — the
	// harness then reads the global api.RAMAdmissionCeilingMB
	// and api.VCPUSlots fallbacks (the legacy single-box
	// posture, safe for pre-multi-node test seams).
	nodeCeilingResolver func(ctx context.Context, nodeID string) (ceilingMB int, vcpuBudget int, err error)
	// specBuilder is the canonical AppSpec constructor the
	// harness uses at Phase 3. Wiring it as a closure (rather
	// than re-implementing the builder here) keeps the spec
	// shape in lock-step with the Wake-time builder at
	// Engine.BuildAppSpecForMigration — a divergence would
	// silently regress the migration (wrong LayerKey →
	// cold-boot from base; wrong VCPU → Scale under-provision;
	// dropped HealthcheckPath → readiness probe skipped;
	// swallowed secrets/env errors → empty guest env.json).
	// Tests inject a stub; production wiring points this at
	// the engine method.
	specBuilder func(ctx context.Context, instanceID string) (AppSpec, error)
	// newOwnerNodeID is the local schedd's owner node — the
	// new owner for every handoff this orchestrator drives.
	// Set at construction; never mutated (a hot-swap would be
	// a Tier B feature).
	newOwnerNodeID string
	// maxPerTick is api.MigrateLiveMaxPerTick (the
	// per-drain-event cap on the parent Engine.MigrateLiveInstances
	// loop). Read at construction time so the per-instance
	// goroutine spawn count is bounded at Engine.MigrateLiveInstances
	// call time, not here.
	maxPerTick int
	// leaseSeconds is api.MigrateLiveLeaseSeconds (the upper
	// bound on the four-phase handoff). Passed to the dying
	// vmmd at Phase 1 via a context.WithTimeout so a
	// stuck-three-phase handoff is cancelled on the wire
	// before the dying vmmd's own lease-expiry timer fires.
	leaseSeconds int
}

// apiMigrationMetrics is the slice of pkg/wire.OpsMetrics the
// harness actually uses. Defined here so the harness doesn't
// import pkg/wire (which would force-test pkg/wire's
// constructors). The real implementation is
// pkg/wire.NewOpsMetrics, whose LiveMigrationDecisions
// accessor returns a prometheus.Counter (which satisfies
// interface{ Inc() }).
type apiMigrationMetrics interface {
	LiveMigrationDecisions(outcome string) prometheus.Counter
}

// NewMigrationHarness builds the orchestrator. The caller
// (cmd/schedd wiring) is responsible for filling newOwnerNodeID
// from FAAS_NODE_NAME resolution and for capping maxPerTick /
// leaseSeconds from the env-overridable limits.
//
// specBuilder is required; NewMigrationHarness panics on a
// nil builder so a missed wiring surfaces at startup rather
// than as silent spec corruption at the first four-phase
// handoff. Production wiring passes Engine.BuildAppSpecForMigration
// (closure-bound to keep pkg/sched import graph one-way).
//
// The metrics parameter is intentionally typed as an interface
// (rather than *api.Limit) so tests can inject a no-op recorder
// without dragging the full pkg/wire registry into the test
// binary. Production wiring passes wire.NewOpsMetrics(...)
// directly.
func NewMigrationHarness(
	ctx context.Context,
	store state.Store,
	vmm RoutedVMM,
	metrics apiMigrationMetrics,
	log *slog.Logger,
	newOwnerNodeID string,
	specBuilder func(ctx context.Context, instanceID string) (AppSpec, error),
	ledger *NodeLedger,
	nodeCeilingResolver func(ctx context.Context, nodeID string) (ceilingMB int, vcpuBudget int, err error),
) *MigrationHarness {
	if specBuilder == nil {
		panic("sched: NewMigrationHarness: specBuilder is nil (migration will silently corrupt AppSpec at first handoff)")
	}
	if ledger == nil {
		panic("sched: NewMigrationHarness: ledger is nil (Tier A5 destination-side slot reservation would silently no-op at Phase 3)")
	}
	h := &MigrationHarness{
		store:               store,
		vmm:                 vmm,
		metrics:             metrics,
		log:                 log,
		specBuilder:         specBuilder,
		newOwnerNodeID:      newOwnerNodeID,
		ledger:              ledger,
		nodeCeilingResolver: nodeCeilingResolver,
		maxPerTick:          api.MigrateLiveMaxPerTick,
		leaseSeconds:        api.MigrateLiveLeaseSeconds,
		events:              nil, // set via WithEvents at wiring time (cmd/schedd)
	}
	// Resolve the destination's row ceiling/budget eagerly so a
	// missing compute_nodes row surfaces at handoff time rather
	// than silently over-admitting the destination (which is the
	// exact regression the comment above is guarding against).
	// nil resolver means "use the legacy fallback" — accepted for
	// pre-multi-node test seams.
	if nodeCeilingResolver != nil && newOwnerNodeID != "" {
		ceilingMB, vcpuBudget, err := nodeCeilingResolver(ctx, newOwnerNodeID)
		if err != nil {
			log.Warn("sched: NewMigrationHarness: node ceiling resolve failed; falling back to global defaults",
				"node_id", newOwnerNodeID, "err", err)
		} else {
			h.destinationCeilingMB = ceilingMB
			h.destinationVCPUBudget = vcpuBudget
		}
	}
	return h
}

// SetMaxPerTick overrides the per-batch cap (tests only). The
// production wiring reads api.MigrateLiveMaxPerTick at
// construction; tests use a smaller cap so a fixture with N
// instances doesn't spawn N goroutines.
func (h *MigrationHarness) SetMaxPerTick(n int) { h.maxPerTick = n }

// SetLeaseSeconds overrides the lease window (tests only).
func (h *MigrationHarness) SetLeaseSeconds(n int) { h.leaseSeconds = n }

// WithEvents installs the recovery-timeline fan-out
// (Workstream B / Task #66). Nil clears the platform (returns
// the receiver for fluent chaining).
func (h *MigrationHarness) WithEvents(p *events.Platform) *MigrationHarness {
	h.events = p
	return h
}

// MigrateOne runs the four-phase handoff for a single
// candidate instance. The parent Engine.MigrateLiveInstances
// goroutine pool calls this once per candidate.
//
// The instanceID + fromNodeID arguments come from the
// ListLiveInstancesOnNode result. The leaseToken is minted by
// the dying vmmd at Phase 1; this function returns the final
// outcome enum so the caller can bump the metric.
//
// The lease clock is enforced at the orchestrator level via a
// context.WithTimeout(leaseSeconds) so a stuck-three-phase
// handoff surfaces as ctx.DeadlineExceeded — the Phase 4
// rollback path picks that up and the metric bumps
// outcome="lease_expired".
func (h *MigrationHarness) MigrateOne(ctx context.Context, instanceID, fromNodeID string) error {
	if instanceID == "" || fromNodeID == "" {
		return fmt.Errorf("sched: migrate one: empty instanceID or fromNodeID")
	}
	// Lease-bounded context: the four-phase flow must complete
	// inside MigrateLiveLeaseSeconds. A deadline-exceeded
	// cancellation fires Phase 4 (the row was never moved to
	// 'migrating' if Phase 2 failed; if Phase 3 is the slow
	// step, the lease is already committed and Phase 4 is a
	// no-op on the wire).
	leaseCtx, cancel := context.WithTimeout(ctx, time.Duration(h.leaseSeconds)*time.Second)
	defer cancel()

	// Phase 1: PrepareLiveMigration on the dying vmmd. The
	// dying vmmd pauses the VM, writes the snapshot to the
	// canonical storage backend, and returns the storage keys
	// + a lease_token (UUIDv4 minted by the dying vmmd so the
	// lease clock is tied to the dying vmmd's lease timer).
	//
	// The snapshot_storage_key is the canonical mem blob key
	// the new owner will pull from after Phase 2. We mint it
	// deterministically here via the same shape imaged uses
	// (snap/<deploymentID>/mem) so the dying vmmd and the new
	// owner agree on the namespace. The vmstate blob is the
	// sibling key (snap/<deploymentID>/vmstate); both keys are
	// returned by the dying vmmd at Phase 1.
	snapshotKey := fmt.Sprintf("snap/migration-%s/mem", instanceID)
	prepared, err := h.vmm.PrepareLiveMigration(leaseCtx, fromNodeID, instanceID, snapshotKey)
	if err != nil {
		// Phase 1 failure: no state has changed on the dying
		// vmmd (it returned before pausing). The instance
		// stays RUNNING on the dying node. Drop silently and
		// bump peer_failure — the next compute_node_changed
		// event retries.
		h.metrics.LiveMigrationDecisions("peer_failure").Inc()
		h.log.Warn("sched: migrate one: Phase 1 prepare failed",
			"instance_id", instanceID,
			"from_node_id", fromNodeID,
			"err", err,
		)
		return fmt.Errorf("sched: migrate one: phase 1 prepare: %w", err)
	}

	// Phase 2: MarkInstanceMigrating on the local store. The
	// conditional UPDATE flips the instance state from
	// 'running' to 'migrating' under the
	// state='running' + node_id=fromNodeID predicate, AND
	// stamps lease_token so the rollback at Phase 4 has a
	// matching predicate. A peer rollback / owner change /
	// row-gone returns ErrConflict.
	if err := h.store.MarkInstanceMigrating(leaseCtx, instanceID, fromNodeID, prepared.LeaseToken); err != nil {
		if errors.Is(err, state.ErrConflict) {
			h.metrics.LiveMigrationDecisions("conflict").Inc()
			h.log.Debug("sched: migrate one: Phase 2 peer conflict",
				"instance_id", instanceID,
				"from_node_id", fromNodeID,
			)
			// Phase 4: tell the dying vmmd to abort the
			// pause and resume the VM. Best-effort.
			_ = h.vmm.CancelLiveMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken)
			return state.ErrConflict
		}
		if errors.Is(err, state.ErrNotFound) {
			// Instance hard-deleted mid-flight. Drop.
			h.log.Warn("sched: migrate one: Phase 2 instance gone",
				"instance_id", instanceID,
			)
			_ = h.vmm.CancelLiveMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken)
			return state.ErrNotFound
		}
		h.metrics.LiveMigrationDecisions("peer_failure").Inc()
		h.log.Warn("sched: migrate one: Phase 2 mark migrating failed",
			"instance_id", instanceID,
			"from_node_id", fromNodeID,
			"err", err,
		)
		_ = h.vmm.CancelLiveMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken)
		return fmt.Errorf("sched: migrate one: phase 2 mark migrating: %w", err)
	}

	// Phase 3: AdoptMigratedInstance on the new owner vmmd.
	// The new owner restores the snapshot the dying vmmd wrote
	// at Phase 1, brings the VM up, and returns the network
	// identifiers. We don't currently persist those on the
	// migration path (the instance row's host_ip is set at
	// wake time and the column stays), but the wire shape
	// carries them so the new owner vmmd's logs can correlate.
	//
	// AppSpec is rebuilt from the local app + deployment view.
	// For the A5 v1 PR the AppSpec shape is built from the
	// Instance's app_id via a best-effort lookup; a future
	// PR will thread the AppSpec through Engine.MigrateLiveInstances
	// to avoid the lookup (the engine already has the App
	// row in hand at the call site).
	appSpec, err := h.loadAppSpecForInstance(leaseCtx, instanceID)
	if err != nil {
		// Phase 3 setup failure (AppSpec couldn't be built).
		// Roll back Phase 2 via Store.CancelInstanceMigration
		// (which restores state='parked' on the original owner)
		// and Phase 4 via the dying vmmd.
		h.metrics.LiveMigrationDecisions("peer_failure").Inc()
		h.log.Warn("sched: migrate one: load app spec failed",
			"instance_id", instanceID,
			"err", err,
		)
		_ = h.store.CancelInstanceMigration(leaseCtx, instanceID, fromNodeID, prepared.LeaseToken)
		_ = h.vmm.CancelLiveMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken)
		return fmt.Errorf("sched: migrate one: load app spec: %w", err)
	}
	// Phase 3 reservation: reserve destination-side RAM + vCPU BEFORE
	// the wire call so a flood of inbound migrations cannot over-admit
	// a destination node (invariant §6.2-2 re-stated per-node).
	// KindMigration deliberately skips per-app concurrency (§6.2-1):
	// the instance was already counted on the source node, so bumping
	// on the destination would double-count and briefly cap an app
	// that's mid-migration. The destination's per-node ceiling +
	// vCPU budget come from the local compute_nodes row
	// (h.newOwnerNodeRow — populated by the Engine at
	// MigrateLiveInstances time, see engine.go:1810-1845); on a
	// single-box deploy that row's AdmissionCeilingMB equals
	// api.RAMAdmissionCeilingMB so the math collapses to the legacy
	// box-wide ceiling (CLAUDE.md invariant 2). The source-side
	// reservation is released by the source vmmd's Park/reaper path
	// on the dying node — schedd's per-node ledger is per-process
	// and the source's ledger lives on the source schedd, not here.
	// The destination's reservation persists on Phase 4 success —
	// the instance is now RUNNING on the destination and counts
	// toward its ledger normally.
	if err := h.ledger.Admit(Request{
		Instance:      instanceID,
		RAMMB:         int(appSpec.MemSizeMiB),
		VCPU:          int(appSpec.VCPUCount),
		Kind:          KindMigration,
		NodeID:        h.newOwnerNodeID,
		NodeCeilingMB: h.destinationCeilingMB,
		VCPUBudget:    h.destinationVCPUBudget,
		// AppID + Plan left zero-valued: KindMigration skips
		// both the per-app concurrency check and the
		// api.LimitsFor lookup (see admission.go Admit).
	}); err != nil {
		// Phase 3 ledger refusal: destination has no headroom.
		// Roll back Phase 2 + Phase 4 (same shape as wire
		// failure below; a different metric label so the §12
		// dashboard can disambiguate capacity refusals from
		// peer dial failures).
		h.metrics.LiveMigrationDecisions("no_headroom").Inc()
		h.log.Warn("sched: migrate one: Phase 3 ledger refused",
			"instance_id", instanceID,
			"new_owner_node_id", h.newOwnerNodeID,
			"err", err,
		)
		_ = h.store.CancelInstanceMigration(leaseCtx, instanceID, fromNodeID, prepared.LeaseToken)
		_ = h.vmm.CancelLiveMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken)
		return fmt.Errorf("sched: migrate one: phase 3 ledger: %w", err)
	}
	adopted, err := h.vmm.AdoptMigratedInstance(leaseCtx, h.newOwnerNodeID, instanceID, appSpec,
		prepared.MemStorageKey, prepared.VMStateStorageKey, prepared.LeaseToken)
	if err != nil {
		// Phase 3 wire failure (new owner dial / restore
		// failed). Roll back Phase 2 + Phase 4 + ledger
		// reservation. The Release must run BEFORE
		// CancelInstanceMigration so a transient store error
		// can't leave the destination ledger over-counted
		// (the gateway's pg_notify path doesn't fire on this
		// branch — we never committed state='migrating' to
		// the row from the destination's perspective).
		h.ledger.Release(instanceID)
		h.metrics.LiveMigrationDecisions("peer_failure").Inc()
		h.log.Warn("sched: migrate one: Phase 3 adopt failed",
			"instance_id", instanceID,
			"new_owner_node_id", h.newOwnerNodeID,
			"err", err,
		)
		_ = h.store.CancelInstanceMigration(leaseCtx, instanceID, fromNodeID, prepared.LeaseToken)
		_ = h.vmm.CancelLiveMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken)
		return fmt.Errorf("sched: migrate one: phase 3 adopt: %w", err)
	}
	_ = adopted // network identifiers are surfaced on the wire but
	// not persisted in this PR; future work can plumb them
	// through the gateway listener if a customer wants
	// zero-downtime migration observability.

	// Phase 4: MigrateInstanceOwner on the local store. The
	// conditional UPDATE flips the instance row: state
	// 'migrating' → 'running', node_id flips to newOwner, the
	// migration lineage columns are stamped, AND
	// apps.migrated_at is stamped in the same transaction.
	if err := h.store.MigrateInstanceOwner(leaseCtx, instanceID, fromNodeID, h.newOwnerNodeID, prepared.LeaseToken); err != nil {
		// Distinguish peer rollback / re-owner / lease
		// expiry / row-gone via errors.Is. Each branch has a
		// different metric label.
		if errors.Is(err, state.ErrConflict) {
			// Peer rollback / re-owner: the row was moved
			// by a concurrent orchestrator. The dying vmmd
			// still has the VM paused; tell it to abort
			// (Phase 4 on the wire). Release the Phase 3
			// ledger reservation first — same reasoning as
			// the Phase 3 wire-failure path: the destination
			// ledger must not over-count the rolled-back
			// migration. No source-side ledger entry to
			// release (the source's ledger lives on the
			// source schedd process).
			h.ledger.Release(instanceID)
			h.metrics.LiveMigrationDecisions("conflict").Inc()
			h.log.Debug("sched: migrate one: Phase 4 peer conflict",
				"instance_id", instanceID,
				"from_node_id", fromNodeID,
			)
			_ = h.vmm.CancelLiveMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken)
			return state.ErrConflict
		}
		if errors.Is(err, state.ErrNotFound) {
			// Hard-deleted mid-flight; same Release reasoning
			// as ErrConflict — the destination's per-node
			// ledger still has the Phase 3 reservation
			// (Admit succeeded; AdoptMigratedInstance
			// returned an instanceID that the row no longer
			// points at). Without Release, the next
			// MigrateLiveInstances tick will see the
			// destination's RAM headroom artificially
			// depressed.
			h.ledger.Release(instanceID)
			h.log.Warn("sched: migrate one: Phase 4 instance gone",
				"instance_id", instanceID,
			)
			_ = h.vmm.CancelLiveMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken)
			return state.ErrNotFound
		}
		// Anything else: lease expiry (ctx.DeadlineExceeded)
		// or a transient DB error. Bump lease_expired on the
		// context error; bump peer_failure on anything else
		// (the operator can disambiguate via slog). Release
		// runs first regardless — same invariant as the
		// ErrConflict / ErrNotFound branches.
		if errors.Is(err, leaseCtx.Err()) || errors.Is(err, context.DeadlineExceeded) {
			h.ledger.Release(instanceID)
			h.metrics.LiveMigrationDecisions("lease_expired").Inc()
			h.log.Warn("sched: migrate one: Phase 4 lease expired",
				"instance_id", instanceID,
				"from_node_id", fromNodeID,
				"lease_seconds", h.leaseSeconds,
			)
			_ = h.vmm.CancelLiveMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken)
			return fmt.Errorf("sched: migrate one: phase 4 lease expired: %w", err)
		}
		h.ledger.Release(instanceID)
		h.metrics.LiveMigrationDecisions("peer_failure").Inc()
		h.log.Warn("sched: migrate one: Phase 4 commit failed",
			"instance_id", instanceID,
			"from_node_id", fromNodeID,
			"err", err,
		)
		_ = h.vmm.CancelLiveMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken)
		return fmt.Errorf("sched: migrate one: phase 4 commit: %w", err)
	}

	// Phase 5: AcknowledgeMigration on the dying vmmd. Best-
	// effort — a non-OK status here is logged Debug and
	// dropped. The dying vmmd will eventually destroy the
	// paused VM on its own lease-expiry timer; the ack is
	// just a "you can free the netns now" hint.
	if err := h.vmm.AcknowledgeMigration(leaseCtx, fromNodeID, instanceID, prepared.LeaseToken); err != nil {
		h.log.Debug("sched: migrate one: Phase 5 ack best-effort failed",
			"instance_id", instanceID,
			"from_node_id", fromNodeID,
			"err", err,
		)
	}

	// Success.
	h.metrics.LiveMigrationDecisions("migrated").Inc()
	h.log.Info("sched: migrate one: success",
		"instance_id", instanceID,
		"from_node_id", fromNodeID,
		"to_node_id", h.newOwnerNodeID,
		"lease_token", prepared.LeaseToken,
	)
	if h.events != nil {
		// InstanceMigrated event: payload uses fields that the
		// AppSpec doesn't carry (AppID/DeploymentID). The
		// dashboard's recovery filter joins via instance_id so
		// the missing app context is fine for audit; a follow-up
		// can plumb it through the store read here.
		h.events.EmitRecovery(ctx, events.InstanceMigratedEvent{
			EmitAt:       time.Now().UTC(),
			InstanceID:   instanceID,
			SourceNodeID: fromNodeID,
			DestNodeID:   h.newOwnerNodeID,
			LeaseID:      string(prepared.LeaseToken),
		})
	}
	return nil
}

// loadAppSpecForInstance delegates to Engine.BuildAppSpecForMigration
// so the spec shape stays in lock-step with the Wake-time builder.
// A divergence here would silently regress the migration: a wrong
// LayerKey cold-boots from the base, a wrong VCPU under-provisions
// Scale-tier apps, a dropped HealthcheckPath skips the readiness
// probe, swallowed secrets/env errors empty the guest's env.json.
//
// Returning a typed error here lets MigrateOne's Phase 3 setup
// branch roll back Phase 2 (state→parked) and fire Phase 4
// (dying-vmmd cancel).
func (h *MigrationHarness) loadAppSpecForInstance(ctx context.Context, instanceID string) (AppSpec, error) {
	if h.specBuilder == nil {
		return AppSpec{}, fmt.Errorf("sched: load app spec: harness has no spec builder (wiring bug)")
	}
	return h.specBuilder(ctx, instanceID)
}
