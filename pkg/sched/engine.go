// engine.go is schedd's wake/park engine: the code that turns a policy decision
// (admit this wake, park that idle instance) into a vmmd RPC plus the single
// authoritative write to the `instances` table. It sits between the pure
// selectors (reaper.go, admission.go) and the microVM (vmmclient.go).
//
// Ownership rules it enforces (CLAUDE.md):
//   - schedd is the ONLY writer to `instances` — every transition goes through
//     e.transition, which validates the state-machine edge (state.CanTransition)
//     before writing.
//   - imaged is the ONLY writer to `snapshots` — a park writes the blob via vmmd
//     then hands the row off with a snapshot_written notification (ADR-018); the
//     engine never inserts a snapshot row itself.
//   - the admission ledger is the single choke point for invariants §6.2-1/2 —
//     nothing boots a VM without an Admit first.

package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// vmmd RPC deadlines (spec §6.1). Centralised here — not in VMMClient —
// because the same client serves every RPC and each has a different
// spec budget. The values are not configurable; they are spec §6.1, not
// operator preference.
const (
	// WakingTimeout is the §6.1 budget for WAKING: "≤ 5s → fall back to
	// cold-boot". 6s = 5s spec + 1s vmmd round trip. The watchdog
	// (commit 3) trips on this same number independently — both stay
	// within ±1s of each other so the watchdog catches a row that
	// sneaks in just before the deadline here.
	WakingTimeout = 6 * time.Second

	// ColdBootTimeout is the §6.1 budget for COLD_BOOTING: "≤ 30s →
	// FAILED". 35s absorbs the vmmd round trip plus jailer setup.
	ColdBootTimeout = 35 * time.Second

	// DestroyTimeout guards the best-effort Destroy calls in the error
	// paths (Wake failed mid-boot, Evict). A hung destroy leaks at
	// worst a stale jail cgroup for 10s — acceptable vs. leaking
	// forever if Firecracker is wedged.
	DestroyTimeout = 10 * time.Second
)

// bootTimeout returns the §6.1 budget for a vmmd call when the row is
// in the given state. Unknown states get the cold-boot budget
// (conservative); never returns zero.
func bootTimeout(s state.State) time.Duration {
	switch s {
	case state.StateWaking:
		return WakingTimeout
	case state.StateColdBooting:
		return ColdBootTimeout
	default:
		return ColdBootTimeout
	}
}

// Notifier is the pg_notify surface the engine needs. db.Notify (pool-backed)
// satisfies it via poolNotifier; tests inject a fake.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// Engine drives wakes and parks. It is safe for concurrent use: all mutation of
// one app's instances is serialised by a per-app lock so a Wake and a reaper
// Park for the same app never race the ledger or the state machine.
type Engine struct {
	store  state.Store
	ledger *Ledger
	vmm    VMM
	notif  Notifier
	fcVer  string // running Firecracker version — snapshots load only on a match (ADR-005)
	log    *slog.Logger
	ops    *wire.OpsMetrics // nil is tolerated by KillStuck (skip the counter increment)

	mu    sync.Mutex
	appMu map[string]*sync.Mutex // app_id -> serialisation lock (never GC'd; one-box scale)
}

// NewEngine wires the engine. notif may be nil (notifications are best-effort in
// tests); log may be nil (slog default); ops may be nil (tests don't assert on
// metrics).
func NewEngine(store state.Store, ledger *Ledger, vmm VMM, notif Notifier, fcVer string, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		store:  store,
		ledger: ledger,
		vmm:    vmm,
		notif:  notif,
		fcVer:  fcVer,
		log:    log,
		appMu:  map[string]*sync.Mutex{},
	}
}

// WithOpsMetrics attaches a metrics bag to the engine for the §6.1
// watchdog's per-(from,to) kill counter and the audit-log write-failure
// counter. Returns the engine for builder-style wiring.
func (e *Engine) WithOpsMetrics(ops *wire.OpsMetrics) *Engine {
	e.ops = ops
	return e
}

// WakeResult is what the gateway needs back from a wake: which instance serves
// the app and at what address.
type WakeResult struct {
	InstanceID string
	Addr       string // host_ip:8080, empty only on error
	Method     vmmdpb.WakeMethod
}

// Wake ensures a running instance for appID and returns its address (spec §4.3
// wake path). Idempotent: an app that already has a RUNNING instance returns it
// without a new boot — this is what lets the gateway's single-flight WakeGate
// hand every coalesced waiter an address. Admission denial returns a *api.Problem
// (capacity / plan concurrency) the gateway maps straight to 503/409.
//
// Lock discipline (commit 2, fixing finding #1 of the M7 audit):
//
//   - Phase 1 — fast path. Under appMu. A second Wake for the same app
//     that races a RUNNING row returns it without a new boot.
//   - Phase 2 — admit window. Under appMu. resolveApp, CreateInstance,
//     emit, ledger.Admit, AppSpec build. Nothing slow.
//   - Phase 3 — DROP THE LOCK around the vmmd RPC. The cold-boot can
//     take up to ColdBootTimeout (35s, spec §6.1) and we must not hold
//     the per-app mutex for the full boot — a reaper Park for the
//     same app, or a second concurrent Wake, would block for that
//     window. The pre-boot state (WAKING or COLD_BOOTING) plus the
//     ledger reservation are the contract: another caller can observe
//     them, but the row is not yet RUNNING so RunningInstanceForApp
//     keeps missing and the second Wake proceeds to its own boot — no
//     double boot race because of the Phase 4 re-read.
//   - Phase 4 — RE-ACQUIRE the lock. Re-read the row under the lock;
//     if the watchdog (commit 3) or a Park stole the state during
//     Phase 3, abort the Wake: release the ledger, destroy the VM we
//     just booted, and surface the error. Otherwise SetInstanceRuntime,
//     transition → RUNNING.
//
// We re-acquire for Phase 4 (rather than commit without the lock)
// because the post-vmmd commit writes a partial row (host_ip, netns,
// guest_uid) and a Park triggered by the reaper reads the row under
// its own appMu; without re-acquiring, the reaper could see a
// partially-written row and act on it.
func (e *Engine) Wake(ctx context.Context, appID string) (WakeResult, error) {
	// ── Phase 1: fast path under appMu ─────────────────────────────
	release := e.lockApp(appID)
	if ins, err := e.store.RunningInstanceForApp(ctx, appID); err == nil {
		release()
		return WakeResult{InstanceID: ins.ID, Addr: instanceAddr(ins.HostIP), Method: vmmdpb.WakeMethod_WAKE_RESTORE}, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		release()
		return WakeResult{}, fmt.Errorf("sched: wake: running lookup: %w", err)
	}

	// ── Phase 2: admit window, still under appMu ──────────────────
	app, acct, limits, dep, err := e.resolveApp(ctx, appID)
	if err != nil {
		release()
		return WakeResult{}, err
	}

	// Restore iff a fresh, version-matched snapshot exists; else cold boot
	// (ADR-005: cold boot always works, snapshot is cache).
	snap, haveSnap := e.usableSnapshot(ctx, dep.ID)

	initState := state.StateColdBooting
	if haveSnap {
		initState = state.StateWaking
	}
	ins, err := e.store.CreateInstance(ctx, appID, dep.ID, string(initState), app.RAMMB)
	if err != nil {
		release()
		return WakeResult{}, fmt.Errorf("sched: wake: create instance: %w", err)
	}
	e.emitInstanceChanged(ctx, ins.ID, appID, initState)

	if err := e.ledger.Admit(Request{
		Instance: ins.ID, AppID: appID, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
	}); err != nil {
		// Admit failed (capacity / concurrency). Lock the row to
		// FAILED before releasing: a concurrent reader must see a
		// coherent final state, not an unattached reservation. Use
		// transitionWithKind so the audit log records this as a
		// wake_boot_error rather than a generic state_transition.
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "admit_denied")
		release()
		return WakeResult{}, err // *api.Problem
	}

	// AppSpec is built under the lock and treated as immutable below.
	// The boot call uses the same spec — the vmmd side reads it
	// thread-safely without us touching it again.
	spec := AppSpec{
		BasePath: basePath(app.Runtime), LayerPath: layerPath(dep.ID),
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB),
		EgressMbit: int32(limits.EgressMbit),
		SealedEnv:  e.loadSealedEnv(ctx, acct.ID, appID),
	}

	// Capture the boot inputs we need across the unlocked window. These
	// are values (not references) — they remain valid after release.
	bootInput := bootInput{
		insID:     ins.ID,
		appID:     appID,
		depID:     dep.ID,
		initState: initState,
		haveSnap:  haveSnap,
		snapID:    snap.ID,
		snapVer:   snap.FCVersion,
		spec:      spec,
	}
	release()

	// ── Phase 3: drop the lock, do the slow vmmd RPC ──────────────
	var out *WakeOutcome
	bootCtx, cancel := context.WithTimeout(ctx, bootTimeout(bootInput.initState))
	defer cancel()
	if bootInput.haveSnap {
		mem, vmstate := snapshotPaths(bootInput.depID)
		out, err = e.vmm.CreateFromSnapshot(bootCtx, bootInput.insID, bootInput.spec, SnapshotRef{
			DeploymentID: bootInput.depID, MemPath: mem, VMStatePath: vmstate, FCVersion: bootInput.snapVer,
		})
	} else {
		out, err = e.vmm.CreateColdBoot(bootCtx, bootInput.insID, bootInput.spec)
	}
	if err != nil {
		// Boot error path. Release the reservation, transition to
		// FAILED. The transition's own re-read will write the row
		// even though we no longer hold the lock — transition is
		// lock-free by design (it only re-reads + writes one row).
		// Audit-log it under kind="wake_boot_error" so a query for
		// `kind='wake_boot_error'` finds both this and the
		// SetInstanceRuntime-failure case below.
		e.ledger.Release(bootInput.insID)
		e.transitionWithKind(ctx, bootInput.insID, bootInput.appID, state.StateFailed, "wake_boot_error", "vmm_boot_failed")
		return WakeResult{}, err
	}

	// A restore that fell back to cold boot means the snapshot is bad:
	// mark it stale so the next wake cold-boots directly and the next
	// park re-snapshots. Best-effort — failure here doesn't block the
	// RUNNING transition (the stale snapshot also gets the next-park
	// treatment from snapshotAndPark).
	if bootInput.haveSnap && out.Method == vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		if err := e.store.MarkSnapshotStale(ctx, bootInput.snapID); err != nil {
			e.log.Warn("wake: mark snapshot stale", "snapshot", bootInput.snapID, "err", err)
		}
		e.log.Info("wake: restore fell back to cold boot", "app", bootInput.appID, "instance", bootInput.insID)
	}

	// ── Phase 4: re-acquire the lock for the post-vmmd commit ────
	release2 := e.lockApp(bootInput.appID)
	defer release2()

	// Re-read the row. If a watchdog (commit 3) or a Park or another
	// Wake moved it out of initState during Phase 3, abort: this Wake
	// is no longer the canonical owner. Free the reservation and
	// destroy the VM we just booted.
	fresh, fresErr := e.store.InstanceByID(ctx, bootInput.insID)
	if fresErr != nil {
		// Couldn't re-read — take the conservative path. Destroy and
		// release; the transition will fail (no row), but the original
		// row must already be gone too (otherwise re-read wouldn't
		// fail).
		e.ledger.Release(bootInput.insID)
		e.bestEffortDestroy(ctx, bootInput.insID)
		return WakeResult{}, fmt.Errorf("sched: wake: re-read instance %s: %w", bootInput.insID, fresErr)
	}
	if fresh.State != string(bootInput.initState) {
		e.ledger.Release(bootInput.insID)
		e.bestEffortDestroy(ctx, bootInput.insID)
		e.log.Warn("wake: state stolen during boot, aborting",
			"app", bootInput.appID, "instance", bootInput.insID,
			"expected", bootInput.initState, "got", fresh.State)
		return WakeResult{}, fmt.Errorf("sched: wake: state stolen by another transition: was %s, now %s", bootInput.initState, fresh.State)
	}

	if err := e.store.SetInstanceRuntime(ctx, bootInput.insID, out.Netns, out.HostIP, int(out.LeaseUID)); err != nil {
		// Booted but unrecordable — destroy to avoid a resource leak,
		// then fail. Best-effort with a hard ceiling: a hung
		// Firecracker can't pin the Wake goroutine forever.
		e.bestEffortDestroy(ctx, bootInput.insID)
		e.ledger.Release(bootInput.insID)
		e.transitionWithKind(ctx, bootInput.insID, bootInput.appID, state.StateFailed, "wake_boot_error", "record_runtime_failed")
		return WakeResult{}, fmt.Errorf("sched: wake: record runtime: %w", err)
	}
	e.transition(ctx, bootInput.insID, bootInput.appID, state.StateRunning)

	return WakeResult{InstanceID: bootInput.insID, Addr: instanceAddr(out.HostIP), Method: out.Method}, nil
}

// bootInput is the immutable bundle of values needed across the
// unlocked window in Wake's Phase 3. Captured under the Phase 2 lock;
// consumed by Phase 3 (vmmd call) and Phase 4 (post-boot commit).
type bootInput struct {
	insID     string
	appID     string
	depID     string
	initState state.State
	haveSnap  bool
	snapID    string // empty when haveSnap is false
	snapVer   string // empty when haveSnap is false
	spec      AppSpec
}

// timedDestroy issues a vmm.Destroy with a hard timeout using a
// detached context. We deliberately use context.Background() rather
// than the caller's ctx so a shutting-down caller (cancelled ctx)
// still gets its destroy cleanup — the destroy is best-effort but
// must not be skipped. The lint exemption lives once, here; every
// caller goes through this helper so future destroy paths can't
// accidentally fall back to the caller's ctx.
//
// The ctx parameter is accepted to satisfy the contextcheck linter's
// expectation that context-using functions take a parent context. We
// deliberately discard it — see the Background() justification above.
// The timeout parameter lets callers pick the deadline (most use
// DestroyTimeout; KillStuck uses a tighter 5s so a wedged Firecracker
// can't pin the watchdog goroutine).
//
// Returns the underlying vmmd error so callers can decide whether
// to log, surface, or discard; the timeout itself is not surfaced.
func (e *Engine) timedDestroy(ctx context.Context, instanceID string, timeout time.Duration) error {
	_ = ctx // see comment above
	destroyCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return e.vmm.Destroy(destroyCtx, instanceID) //nolint:contextcheck // shutdown context is intentionally detached from the already-cancelled caller ctx.
}

// bestEffortDestroy is the no-error-discard wrapper around
// timedDestroy at the standard DestroyTimeout, used by Phase 4 /
// Prime error paths where the destroy failure is observation-only
// and the row is already doomed.
func (e *Engine) bestEffortDestroy(ctx context.Context, instanceID string) {
	_ = e.timedDestroy(ctx, instanceID, DestroyTimeout)
}

// Prime boots a freshly-built deployment once, snapshots it, and parks it —
// step 6 of the deploy pipeline (spec §5). schedd runs it on imaged's
// snapshot_prime handshake (ADR-018); on success it emits snapshot_written so
// imaged records the snapshot row and marks the deployment live.
func (e *Engine) Prime(ctx context.Context, appID, deploymentID string) error {
	release := e.lockApp(appID)
	defer release()

	app, acct, limits, err := e.resolveAppForDeploy(ctx, appID)
	if err != nil {
		return err
	}

	ins, err := e.store.CreateInstance(ctx, appID, deploymentID, string(state.StateColdBooting), app.RAMMB)
	if err != nil {
		return fmt.Errorf("sched: prime: create instance: %w", err)
	}
	e.emitInstanceChanged(ctx, ins.ID, appID, state.StateColdBooting)

	if err := e.ledger.Admit(Request{
		Instance: ins.ID, AppID: appID, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
	}); err != nil {
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "prime_admit_denied")
		return err
	}

	spec := AppSpec{
		BasePath: basePath(app.Runtime), LayerPath: layerPath(deploymentID),
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB),
		EgressMbit: int32(limits.EgressMbit),
		SealedEnv:  e.loadSealedEnv(ctx, acct.ID, appID),
	}
	// Per-call deadline (commit 1, spec §6.1). Same rationale as Wake:
	// Prime's vmmd call gets the ColdBootTimeout budget — a Prime
	// that takes longer is dead and the operator should restart
	// imaged's pipeline, not wait for a hung Firecracker.
	bootCtx, pcancel := context.WithTimeout(ctx, bootTimeout(state.StateColdBooting))
	defer pcancel()
	out, err := e.vmm.CreateColdBoot(bootCtx, ins.ID, spec)
	if err != nil {
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "prime_cold_boot_failed")
		return fmt.Errorf("sched: prime: cold boot: %w", err)
	}
	if err := e.store.SetInstanceRuntime(ctx, ins.ID, out.Netns, out.HostIP, int(out.LeaseUID)); err != nil {
		// Best-effort destroy; same rationale as Wake above. Uses a
		// detached context so a cancelled caller ctx doesn't make the
		// destroy fire-and-forget (it would still need its own
		// timeout).
		e.bestEffortDestroy(ctx, ins.ID)
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "prime_record_runtime_failed")
		return fmt.Errorf("sched: prime: record runtime: %w", err)
	}
	e.transition(ctx, ins.ID, appID, state.StateRunning)

	// Boot succeeded; snapshot + park it (the prime is not left running).
	ins.AppID, ins.DeploymentID = appID, deploymentID
	return e.snapshotAndPark(ctx, ins)
}

// Park snapshots a RUNNING instance and frees its RAM (idle reaper, spec §4.3).
// Acquires the app lock; the reaper calls it per selected instance. The reaper
// builds its selection without the lock, so we re-read under the lock and skip
// anything no longer RUNNING (a concurrent wake/park already moved it).
func (e *Engine) Park(ctx context.Context, instanceID string) error {
	ins, err := e.lockedRunning(ctx, instanceID)
	if err != nil || ins == nil {
		return err
	}
	defer e.unlockApp(ins.AppID)
	return e.snapshotAndPark(ctx, *ins)
}

// ParkWithReason is the meterd-triggered variant (M7, spec §4.7). It
// delegates to Park and stamps a structured log line with the reason
// ("quota_exceeded_free", "manual_admin", etc) so the audit trail can
// answer "why was this instance parked?" without grepping the code.
func (e *Engine) ParkWithReason(ctx context.Context, instanceID, reason string) error {
	err := e.Park(ctx, instanceID)
	if err != nil {
		e.log.Warn("sched: park_with_reason failed", "instance", instanceID, "reason", reason, "err", err)
		return err
	}
	e.log.Info("sched: park_with_reason", "instance", instanceID, "reason", reason)
	return nil
}

// Evict destroys a RUNNING instance under RAM pressure (spec §4.3). Unlike Park
// it does not snapshot — the next wake cold-boots (ADR-005), so the state lands
// in STOPPED rather than PARKED.
func (e *Engine) Evict(ctx context.Context, instanceID string) error {
	ins, err := e.lockedRunning(ctx, instanceID)
	if err != nil || ins == nil {
		return err
	}
	defer e.unlockApp(ins.AppID)

	// Per-call deadline (commit 1). Evict is RAM-pressure, so a wedged
	// Destroy cannot pin the reaper — the deadline frees it. Using a
	// detached context for the same reason as the Wake/Prime error
	// paths: a shutting-down reaper should still get its destroy
	// cleanup.
	if err := e.timedDestroy(ctx, instanceID, DestroyTimeout); err != nil {
		return fmt.Errorf("sched: evict: destroy %s: %w", instanceID, err)
	}
	e.ledger.Release(instanceID)
	e.transition(ctx, instanceID, ins.AppID, state.StateStopped)
	return nil
}

// lockedRunning loads an instance, takes its app lock, and returns it only if it
// is still RUNNING under the lock. A (nil, nil) return means "not RUNNING, skip"
// and the app lock has already been released. On a real error the lock is not
// held. Callers that get a non-nil instance own the lock and must unlockApp.
func (e *Engine) lockedRunning(ctx context.Context, instanceID string) (*state.Instance, error) {
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("sched: load instance %s: %w", instanceID, err)
	}
	e.lockApp(ins.AppID)
	fresh, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		e.unlockApp(ins.AppID)
		return nil, fmt.Errorf("sched: reload instance %s: %w", instanceID, err)
	}
	if fresh.State != string(state.StateRunning) {
		e.unlockApp(ins.AppID)
		return nil, nil
	}
	return &fresh, nil
}

// ReportActivity persists a batch of last_request_at touches from the gateway
// (spec §4.1, ADR-018). schedd is the sole writer to instances, so the gateway
// hands it the batch instead of writing directly.
func (e *Engine) ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error) {
	return e.store.TouchInstancesLastSeen(ctx, touches)
}

// SeedLedger rebuilds the admission ledger from live instance rows at startup so
// the RAM/concurrency accounting survives a schedd restart (spec §4.3). Called
// once by cmd/schedd before the loop starts serving.
func (e *Engine) SeedLedger(ctx context.Context) error {
	apps, err := e.store.ListAllApps(ctx)
	if err != nil {
		return fmt.Errorf("sched: seed ledger: list apps: %w", err)
	}
	for _, app := range apps {
		acct, err := e.store.AccountByID(ctx, app.AccountID)
		if err != nil {
			continue
		}
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok {
			continue
		}
		instances, err := e.store.ListInstancesForApp(ctx, app.ID)
		if err != nil {
			continue
		}
		for _, ins := range instances {
			if !state.State(ins.State).CountsForRAM() {
				continue
			}
			if err := e.ledger.Admit(Request{
				Instance: ins.ID, AppID: app.ID, Plan: acct.Plan,
				RAMMB: ins.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
			}); err != nil {
				e.log.Warn("seed ledger: admit", "instance", ins.ID, "err", err)
				continue
			}
			// SNAPSHOTTING is resident but no longer counts toward concurrency.
			if state.State(ins.State) == state.StateSnapshotting {
				e.ledger.BeginSnapshot(ins.ID)
			}
		}
	}
	return nil
}

// snapshotAndPark is the unlocked park core (caller holds the app lock). It
// walks RUNNING → SNAPSHOTTING → PARKED, writing the snapshot blob via vmmd and
// emitting snapshot_written for imaged to record the row.
func (e *Engine) snapshotAndPark(ctx context.Context, ins state.Instance) error {
	mem, vmstate := snapshotPaths(ins.DeploymentID)
	e.ledger.BeginSnapshot(ins.ID) // drops concurrency, keeps RAM (§6.2-1 excludes snapshotting)
	// Stamp parked_at on entry into SNAPSHOTTING so the §6.1 watchdog
	// (commit 3) has an "age of state" anchor for the row.
	now := time.Now()
	if err := e.store.UpdateInstanceStateWithTimestamp(ctx, ins.ID, string(state.StateSnapshotting), now); err != nil {
		e.log.Warn("snapshotAndPark: stamp parked_at", "instance", ins.ID, "err", err)
		// Fall through to the normal path — the watchdog's beginSnapshot
		// anchor being lost is recoverable (it'll trip after
		// started_at + 20s, slightly inflating the budget).
	}
	e.emitInstanceChanged(ctx, ins.ID, ins.AppID, state.StateSnapshotting)

	b, err := e.vmm.PauseAndSnapshot(ctx, ins.ID, mem, vmstate)
	if err != nil {
		// Snapshot failed (disk?) — free RAM and land in STOPPED; next wake
		// cold-boots (ADR-005). The app still has a cold-bootable rootfs (§6.2-3).
		// Audit-log it as park_snapshot_error (per the kind taxonomy) so
		// "all park-snapshot failures in the last hour" is queryable.
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, ins.AppID, state.StateStopped, "park_snapshot_error", "snapshot_failed")
		return fmt.Errorf("sched: park: snapshot %s: %w", ins.ID, err)
	}
	e.ledger.Release(ins.ID)
	e.transition(ctx, ins.ID, ins.AppID, state.StateParked)
	e.emitSnapshotWritten(ctx, ins.DeploymentID, mem, vmstate, b)
	return nil
}

// resolveApp loads the app, account, plan limits, and current live deployment a
// wake needs. A missing live deployment is a *api.Problem (an app should always
// have one, invariant §6.2-3).
func (e *Engine) resolveApp(ctx context.Context, appID string) (state.App, state.Account, api.Limits, state.Deployment, error) {
	app, acct, limits, err := e.resolveAppForDeploy(ctx, appID)
	if err != nil {
		return state.App{}, state.Account{}, api.Limits{}, state.Deployment{}, err
	}
	dep, err := e.store.LiveDeployment(ctx, appID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.App{}, state.Account{}, api.Limits{}, state.Deployment{},
				api.NewProblem(404, api.CodeNotFound, "No live deployment",
					"the app has no live deployment to wake")
		}
		return state.App{}, state.Account{}, api.Limits{}, state.Deployment{},
			fmt.Errorf("sched: resolve app: live deployment: %w", err)
	}
	return app, acct, limits, dep, nil
}

func (e *Engine) resolveAppForDeploy(ctx context.Context, appID string) (state.App, state.Account, api.Limits, error) {
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return state.App{}, state.Account{}, api.Limits{}, fmt.Errorf("sched: resolve app: %w", err)
	}
	acct, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return state.App{}, state.Account{}, api.Limits{}, fmt.Errorf("sched: resolve app: account: %w", err)
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return state.App{}, state.Account{}, api.Limits{}, fmt.Errorf("sched: resolve app: unknown plan %q", acct.Plan)
	}
	return app, acct, limits, nil
}

// loadSealedEnv reads the per-app sealed env rows and flattens them into
// the fcvm shape Manager.Wake consumes. A read failure here is non-fatal:
// it logs and returns nil (an empty SealedEnv). That preserves the
// "wake succeeds even if PG has a hiccup" property — the app comes up
// without secrets rather than failing entirely. vmmd never sees a stale
// ciphertext, so there's nothing to leak; the worst case is a missing
// secret, which customer support can spot from the next failed deploy.
//
// We carry AccountID explicitly so a cross-account (accountID, appID) pair
// returns ErrNotFound (consistent with apid's 404 contract).
func (e *Engine) loadSealedEnv(ctx context.Context, accountID, appID string) []fcvm.SealedEnvEntry {
	rows, err := e.store.ListAppSecrets(ctx, accountID, appID)
	if err != nil {
		e.log.Warn("load sealed env", "app", appID, "err", err)
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	out := make([]fcvm.SealedEnvEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, fcvm.SealedEnvEntry{Key: r.Key, Ciphertext: r.Ciphertext})
	}
	return out
}

// usableSnapshot returns the freshest non-stale snapshot for a deployment iff it
// was made with the running Firecracker version (ADR-005 pinning).
func (e *Engine) usableSnapshot(ctx context.Context, deploymentID string) (state.Snapshot, bool) {
	snap, err := e.store.LatestSnapshot(ctx, deploymentID)
	if err != nil || snap.Stale || snap.FCVersion != e.fcVer {
		return state.Snapshot{}, false
	}
	return snap, true
}

// StuckReason is the watchdog's reason for forcing a transition
// (spec §6.1 budgets: WAKING ≤5s, COLD_BOOTING ≤30s, SNAPSHOTTING ≤20s).
// Each constant maps to one {from, to} terminal state pair in
// KillStuck. The values are stable (wire format for the audit log + the
// ops metric labels).
type StuckReason string

const (
	StuckWakingTimeout   StuckReason = "waking_timeout"
	StuckColdBootTimeout StuckReason = "cold_boot_timeout"
	StuckSnapshotTimeout StuckReason = "snapshot_timeout"
)

// expectedStateForReason returns the source state the row must be in
// for the supplied timeout reason. Used by KillStuck's pre-check.
func expectedStateForReason(r StuckReason) state.State {
	switch r {
	case StuckWakingTimeout:
		return state.StateWaking
	case StuckColdBootTimeout:
		return state.StateColdBooting
	case StuckSnapshotTimeout:
		return state.StateSnapshotting
	default:
		return ""
	}
}

// terminalStateForReason picks the spec §6.1 transition target:
//   - WAKING → COLD_BOOTING (the "fall back" branch; we abandon this
//     row and let the next wake start a fresh cold-boot).
//   - COLD_BOOTING → FAILED.
//   - SNAPSHOTTING → STOPPED.
func terminalStateForReason(r StuckReason) state.State {
	switch r {
	case StuckWakingTimeout:
		return state.StateColdBooting
	case StuckColdBootTimeout:
		return state.StateFailed
	case StuckSnapshotTimeout:
		return state.StateStopped
	default:
		return ""
	}
}

// KillStuck is the spec §6.1 watchdog's terminal action on a stuck
// row. It runs under appMu, re-reads the row, and only acts if the
// state matches the reason's source state (a Wake / Park that
// completed during the watchdog's planning time must not be
// double-killed). The fast path returns nil for the no-op case so a
// goroutine that just raced us is safe.
//
// KillStuck releases the ledger reservation (idempotent), best-effort
// destroys the vmmd-side VM with a 5s deadline (a wedged Firecracker
// can't pin the watchdog goroutine forever), and finally writes the
// terminal state via transition — which is itself the audit-log
// entrypoint once commit 4 lands.
func (e *Engine) KillStuck(ctx context.Context, instanceID, appID string, reason StuckReason) error {
	if reason != StuckWakingTimeout && reason != StuckColdBootTimeout && reason != StuckSnapshotTimeout {
		return fmt.Errorf("sched: KillStuck: unknown reason %q", reason)
	}

	release := e.lockApp(appID)
	defer release()

	fresh, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		// Row gone — someone else (or a prior watchdog pass) already
		// cleaned up. The reservation may also be gone; Ledger.Release
		// is a no-op on unknown instances (admission.go:117).
		e.ledger.Release(instanceID)
		return nil //nolint:nilerr // state.ErrNotFound is a successful no-op here
	}

	want := expectedStateForReason(reason)
	if state.State(fresh.State) != want {
		// Race: a Wake / Park / prior watchdog already moved the row.
		// Don't second-guess — release the reservation in case it
		// leaked, but do not touch the state machine.
		e.ledger.Release(instanceID)
		return nil
	}

	terminal := terminalStateForReason(reason)

	// Free the ledger reservation first so a parallel Wake for the
	// same app can admit a new instance immediately. Release is
	// idempotent (admission.go:117).
	e.ledger.Release(instanceID)

	// Best-effort destroy. A wedged Firecracker can't pin the
	// watchdog goroutine past the 5s ceiling. Use Background so a
	// cancelled tick ctx doesn't cause us to skip the destroy.
	if err := e.timedDestroy(ctx, instanceID, 5*time.Second); err != nil {
		e.log.Warn("watchdog: destroy failed (best-effort)", "instance", instanceID, "reason", reason, "err", err)
	}

	// Final state write + audit-log emission. transitionWithKind
	// (commit 4) handles the events row's AppendEvent call as part
	// of the normal transition path; we just supply the kind and
	// reason so the audit row is searchable on `kind='watchdog_timeout'`.
	e.transitionWithKind(ctx, instanceID, appID, terminal, "watchdog_timeout", string(reason))
	if e.ops != nil {
		e.ops.WatchdogKills(string(reason), string(terminal)).Inc()
	}
	return nil
}

// transition validates and applies one instance state change, then emits
// instance_changed. An illegal edge is logged and dropped rather than written —
// schedd must never persist an impossible transition (spec §6.1).
//
// Commit 4 also writes the events audit-log row (spec §6.1: "every
// transition is an events row"). The events write is best-effort —
// the state row is the source of truth, the events table is audit.
// A failure here logs a warning and increments the
// events_write_failures counter; the transition itself still
// succeeded.
//
// `reason` is an opaque label for the cause ("watchdog_timeout",
// "wake_boot_error", …) carried in the events row's data payload.
// The default kind is "state_transition" — the only other kind
// reserved today is "watchdog_timeout" (set by KillStuck).
func (e *Engine) transition(ctx context.Context, instanceID, appID string, to state.State) {
	e.transitionWithKind(ctx, instanceID, appID, to, "state_transition", "")
}

// transitionWithKind is the audit-log-emitting variant of transition.
// Callers that need a non-default kind (Wake's "wake_boot_error" path,
// KillStuck's "watchdog_timeout", snapshotAndPark's "park_snapshot_error")
// go through here. The transition body itself is unchanged from
// transition() — only the appended events row differs.
func (e *Engine) transitionWithKind(ctx context.Context, instanceID, appID string, to state.State, kind, reason string) {
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		e.log.Warn("transition: load instance", "instance", instanceID, "to", to, "err", err)
		return
	}
	from := state.State(ins.State)
	if from == to {
		return
	}
	if !state.CanTransition(from, to) {
		e.log.Error("transition: illegal edge refused", "instance", instanceID, "from", from, "to", to)
		return
	}
	if err := e.store.UpdateInstanceState(ctx, instanceID, string(to)); err != nil {
		e.log.Warn("transition: write", "instance", instanceID, "to", to, "err", err)
		return
	}
	e.emitInstanceChanged(ctx, instanceID, appID, to)

	// Audit-log emission (spec §6.1). Best-effort: a failure logs
	// and counts, never rolls back the transition. The state row is
	// the source of truth; this is observation.
	subject := instanceID
	data, _ := json.Marshal(map[string]any{
		"from": string(from), "to": string(to), "reason": reason, "ts": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err := e.store.AppendEvent(ctx, "schedd", kind, &subject, data); err != nil {
		e.log.Warn("transition: append event", "instance", instanceID, "from", from, "to", to, "kind", kind, "err", err)
		if e.ops != nil {
			e.ops.EventsWriteFailures().Inc()
		}
	}
}

func (e *Engine) emitInstanceChanged(ctx context.Context, instanceID, appID string, st state.State) {
	if e.notif == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{"instance_id": instanceID, "app_id": appID, "state": string(st)})
	if err := e.notif.Notify(ctx, db.NotifyInstanceChanged, string(payload)); err != nil {
		e.log.Warn("emit instance_changed", "instance", instanceID, "err", err)
	}
}

func (e *Engine) emitSnapshotWritten(ctx context.Context, deploymentID, memPath, vmstatePath string, b SnapshotBytes) {
	if e.notif == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"deployment_id": deploymentID,
		"mem_path":      memPath,
		"vmstate_path":  vmstatePath,
		"mem_bytes":     b.MemBytes,
		"vmstate_bytes": b.VMStateBytes,
		"fc_version":    e.fcVer,
	})
	if err := e.notif.Notify(ctx, db.NotifySnapshotWritten, string(payload)); err != nil {
		e.log.Warn("emit snapshot_written", "deployment", deploymentID, "err", err)
	}
}

func (e *Engine) lockApp(appID string) func() {
	e.appMutex(appID).Lock()
	return func() { e.unlockApp(appID) }
}

func (e *Engine) unlockApp(appID string) {
	e.appMutex(appID).Unlock()
}

// appMutex returns the stable per-app serialisation mutex, creating it on first
// use. Never GC'd (one-box scale, single-digit apps).
func (e *Engine) appMutex(appID string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	mu, ok := e.appMu[appID]
	if !ok {
		mu = &sync.Mutex{}
		e.appMu[appID] = mu
	}
	return mu
}

func instanceAddr(hostIP string) string {
	if hostIP == "" {
		return ""
	}
	return net.JoinHostPort(hostIP, strconv.Itoa(netns.AppPort))
}

// Ledger exposes the engine's admission ledger for the reaper's resident-RAM
// read and for daemon heartbeat logging.
func (e *Engine) Ledger() *Ledger { return e.ledger }

// Store exposes the engine's Store so the Loop can build the reaper's
// read-only instance snapshot and read crons.
func (e *Engine) Store() state.Store { return e.store }

// Notifier returns the pg_notify notifier the engine writes through.
// nil-safe: returns a noop when the engine was wired without one
// (tests), so callers don't need to nil-check.
func (e *Engine) Notifier() Notifier {
	if e.notif == nil {
		return noopNotifier{}
	}
	return e.notif
}

// noopNotifier discards every notification. Tests use it; production
// always wires the real pgx-backed notifier in cmd/schedd.
type noopNotifier struct{}

func (noopNotifier) Notify(_ context.Context, _ string, _ string) error { return nil }

// PoolNotifier adapts a pgx pool to the Notifier interface (pg_notify). cmd/schedd
// wires one; the engine and tests depend only on the interface.
type PoolNotifier struct{ Pool *pgxpool.Pool }

func (p PoolNotifier) Notify(ctx context.Context, channel, payload string) error {
	return db.Notify(ctx, p.Pool, channel, payload)
}
