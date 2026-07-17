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

	"github.com/jackc/pgx/v5/pgxpool"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
)

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

	mu    sync.Mutex
	appMu map[string]*sync.Mutex // app_id -> serialisation lock (never GC'd; one-box scale)
}

// NewEngine wires the engine. notif may be nil (notifications are best-effort in
// tests); log may be nil (slog default).
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
func (e *Engine) Wake(ctx context.Context, appID string) (WakeResult, error) {
	release := e.lockApp(appID)
	defer release()

	// Idempotent fast path.
	if ins, err := e.store.RunningInstanceForApp(ctx, appID); err == nil {
		return WakeResult{InstanceID: ins.ID, Addr: instanceAddr(ins.HostIP), Method: vmmdpb.WakeMethod_WAKE_RESTORE}, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return WakeResult{}, fmt.Errorf("sched: wake: running lookup: %w", err)
	}

	app, acct, limits, dep, err := e.resolveApp(ctx, appID)
	if err != nil {
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
		return WakeResult{}, fmt.Errorf("sched: wake: create instance: %w", err)
	}
	e.emitInstanceChanged(ctx, ins.ID, appID, initState)

	if err := e.ledger.Admit(Request{
		Instance: ins.ID, AppID: appID, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
	}); err != nil {
		e.transition(ctx, ins.ID, appID, state.StateFailed)
		return WakeResult{}, err // *api.Problem
	}

	spec := AppSpec{
		BasePath: basePath(app.Runtime), LayerPath: layerPath(dep.ID),
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB),
		EgressMbit: int32(limits.EgressMbit),
	}
	var out *WakeOutcome
	if haveSnap {
		mem, vmstate := snapshotPaths(dep.ID)
		out, err = e.vmm.CreateFromSnapshot(ctx, ins.ID, spec, SnapshotRef{
			DeploymentID: dep.ID, MemPath: mem, VMStatePath: vmstate, FCVersion: snap.FCVersion,
		})
	} else {
		out, err = e.vmm.CreateColdBoot(ctx, ins.ID, spec)
	}
	if err != nil {
		e.ledger.Release(ins.ID)
		e.transition(ctx, ins.ID, appID, state.StateFailed)
		return WakeResult{}, err
	}

	// A restore that fell back to cold boot means the snapshot is bad: mark it
	// stale so the next wake cold-boots directly and the next park re-snapshots.
	if haveSnap && out.Method == vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		if err := e.store.MarkSnapshotStale(ctx, snap.ID); err != nil {
			e.log.Warn("wake: mark snapshot stale", "snapshot", snap.ID, "err", err)
		}
		e.log.Info("wake: restore fell back to cold boot", "app", appID, "instance", ins.ID)
	}

	if err := e.store.SetInstanceRuntime(ctx, ins.ID, out.Netns, out.HostIP, int(out.LeaseUID)); err != nil {
		// Booted but unrecordable — destroy to avoid a resource leak, then fail.
		_ = e.vmm.Destroy(ctx, ins.ID)
		e.ledger.Release(ins.ID)
		e.transition(ctx, ins.ID, appID, state.StateFailed)
		return WakeResult{}, fmt.Errorf("sched: wake: record runtime: %w", err)
	}
	e.transition(ctx, ins.ID, appID, state.StateRunning)

	return WakeResult{InstanceID: ins.ID, Addr: instanceAddr(out.HostIP), Method: out.Method}, nil
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
		e.transition(ctx, ins.ID, appID, state.StateFailed)
		return err
	}

	spec := AppSpec{
		BasePath: basePath(app.Runtime), LayerPath: layerPath(deploymentID),
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB),
		EgressMbit: int32(limits.EgressMbit),
	}
	out, err := e.vmm.CreateColdBoot(ctx, ins.ID, spec)
	if err != nil {
		e.ledger.Release(ins.ID)
		e.transition(ctx, ins.ID, appID, state.StateFailed)
		return fmt.Errorf("sched: prime: cold boot: %w", err)
	}
	if err := e.store.SetInstanceRuntime(ctx, ins.ID, out.Netns, out.HostIP, int(out.LeaseUID)); err != nil {
		_ = e.vmm.Destroy(ctx, ins.ID)
		e.ledger.Release(ins.ID)
		e.transition(ctx, ins.ID, appID, state.StateFailed)
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

	if err := e.vmm.Destroy(ctx, instanceID); err != nil {
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
	e.transition(ctx, ins.ID, ins.AppID, state.StateSnapshotting)

	b, err := e.vmm.PauseAndSnapshot(ctx, ins.ID, mem, vmstate)
	if err != nil {
		// Snapshot failed (disk?) — free RAM and land in STOPPED; next wake
		// cold-boots (ADR-005). The app still has a cold-bootable rootfs (§6.2-3).
		e.ledger.Release(ins.ID)
		e.transition(ctx, ins.ID, ins.AppID, state.StateStopped)
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

// usableSnapshot returns the freshest non-stale snapshot for a deployment iff it
// was made with the running Firecracker version (ADR-005 pinning).
func (e *Engine) usableSnapshot(ctx context.Context, deploymentID string) (state.Snapshot, bool) {
	snap, err := e.store.LatestSnapshot(ctx, deploymentID)
	if err != nil || snap.Stale || snap.FCVersion != e.fcVer {
		return state.Snapshot{}, false
	}
	return snap, true
}

// transition validates and applies one instance state change, then emits
// instance_changed. An illegal edge is logged and dropped rather than written —
// schedd must never persist an impossible transition (spec §6.1).
func (e *Engine) transition(ctx context.Context, instanceID, appID string, to state.State) {
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
