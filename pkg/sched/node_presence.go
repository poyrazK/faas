package sched

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const nodePresenceConfirmReports = 2
const nodePresenceRefreshReports = 30

type nodePresenceReport struct {
	fingerprint           string
	consecutive           int
	reconciledFingerprint string
	reconciledAt          int
	sampledAt             time.Time
	receivedAt            time.Time
}

type nodePresenceObservation struct {
	fingerprint string
	reported    map[string]struct{}
	shouldCheck bool
}

type nodePresenceTracker struct {
	mu      sync.Mutex
	reports map[string]nodePresenceReport
}

func newNodePresenceTracker() *nodePresenceTracker {
	return &nodePresenceTracker{reports: make(map[string]nodePresenceReport)}
}

// Observe confirms distinct, fresh, complete process inventories. Unknown or
// interrupted reporting resets confirmation; rereading one frame is not proof.
func (t *nodePresenceTracker) Observe(inventory NodeInstanceInventory, now time.Time) (nodePresenceObservation, bool) {
	if t == nil || inventory.NodeID == "" {
		return nodePresenceObservation{}, false
	}
	nodeID := inventory.NodeID
	fingerprint, reported, complete := completeNodePresence(inventory.InstanceIDs)
	age := now.Sub(inventory.SampledAt)
	valid := inventory.Complete && complete && age >= -TelemetryFreshness && age <= TelemetryFreshness
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reports == nil {
		t.reports = make(map[string]nodePresenceReport)
	}
	if !valid {
		delete(t.reports, nodeID)
		return nodePresenceObservation{}, false
	}
	report := t.reports[nodeID]
	if !inventory.SampledAt.After(report.sampledAt) {
		return nodePresenceObservation{}, false
	}
	if report.fingerprint == fingerprint && now.Sub(report.receivedAt) <= TelemetryFreshness {
		report.consecutive++
	} else {
		report = nodePresenceReport{fingerprint: fingerprint, consecutive: 1}
	}
	report.sampledAt = inventory.SampledAt
	report.receivedAt = now
	shouldCheck := report.consecutive >= nodePresenceConfirmReports &&
		(report.reconciledAt == 0 || report.reconciledFingerprint != fingerprint ||
			report.consecutive-report.reconciledAt >= nodePresenceRefreshReports)
	t.reports[nodeID] = report
	if !shouldCheck {
		return nodePresenceObservation{}, false
	}
	return nodePresenceObservation{fingerprint: fingerprint, reported: reported, shouldCheck: true}, true
}

func (t *nodePresenceTracker) markReconciled(nodeID, fingerprint string) {
	if t == nil || nodeID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	report, ok := t.reports[nodeID]
	if !ok || report.fingerprint != fingerprint {
		return
	}
	report.reconciledFingerprint = fingerprint
	report.reconciledAt = report.consecutive
	t.reports[nodeID] = report
}

func completeNodePresence(ids []string) (string, map[string]struct{}, bool) {
	reported := make(map[string]struct{}, len(ids))
	sortedIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			return "", nil, false
		}
		if _, duplicate := reported[id]; duplicate {
			return "", nil, false
		}
		reported[id] = struct{}{}
		sortedIDs = append(sortedIDs, id)
	}
	sort.Strings(sortedIDs)
	// Preserve ID boundaries; delimiters within an ID must not make two
	// different inventories look like consecutive identical observations.
	fingerprint, _ := json.Marshal(sortedIDs)
	return string(fingerprint), reported, true
}

// current rejects an observation superseded while waiting for a lifecycle lock.
func (t *nodePresenceTracker) current(nodeID, fingerprint string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	report, ok := t.reports[nodeID]
	return ok && report.fingerprint == fingerprint && report.consecutive >= nodePresenceConfirmReports && now.Sub(report.receivedAt) <= TelemetryFreshness
}

type staleInstanceStore interface {
	FailRunningInstanceIfOwnedByNode(ctx context.Context, id, nodeID string, terminalAt time.Time) error
}

// nodeInventoryRunningLimit bounds one reconciliation read. A node runs far
// fewer instances; a truncated read only repairs the rows it returned.
const nodeInventoryRunningLimit = 4096

// NodeInventoryEnforceEnv controls canary enablement independently of the
// older metric-based divergence sweep, which remains report-only by default.
const NodeInventoryEnforceEnv = "FAAS_SCHEDD_NODE_INVENTORY_ENFORCE"

// ObserveNodeInventory repairs confirmed missing RUNNING instances. The gRPC
// boundary verifies signatures; unknown/legacy reports never assert absence.
func (e *Engine) ObserveNodeInventory(ctx context.Context, inventory NodeInstanceInventory) {
	if e == nil || e.store == nil || e.nodePresence == nil {
		return
	}
	nodeID := inventory.NodeID
	now := e.now().UTC()
	observation, ok := e.nodePresence.Observe(inventory, now)
	if !ok || !observation.shouldCheck {
		return
	}

	reconciler, ok := e.store.(staleInstanceStore)
	if !ok {
		e.log.Warn("sched: stale-instance reconciliation unavailable",
			"node_id", nodeID)
		e.nodePresence.markReconciled(nodeID, observation.fingerprint)
		return
	}

	if nodeID == "" {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Only RUNNING rows can be repaired. Listing every instance the node
	// ever hosted read 21,067 rows on production-us after a boot loop and
	// missed the 2 s deadline on every report, so this repair never ran
	// (hunt #10).
	instances, err := e.store.ListLiveInstancesOnNode(checkCtx, nodeID, nodeInventoryRunningLimit)
	if err != nil {
		e.log.Warn("sched: stale-instance reconciliation list failed",
			"node_id", nodeID, "err", err)
		return
	}

	terminalAt := now
	retry := false
	reconciled := 0
	attempted := 0
	var frozenGone []string
	for _, ins := range instances {
		if ins.State == string(state.StateFrozen) {
			// ADR-1151: a frozen VM missing from the node's signed inventory
			// is gone (vmmd or the node restarted). Its deployment has a
			// snapshot, so parking the row is safe even if the VM survived.
			if _, present := observation.reported[ins.ID]; !present {
				frozenGone = append(frozenGone, ins.ID)
			}
			continue
		}
		if ins.State != string(state.StateRunning) {
			continue
		}
		if _, present := observation.reported[ins.ID]; present {
			continue
		}
		if !ins.StartedAt.IsZero() && now.Sub(ins.StartedAt) < time.Duration(api.InstanceDivergenceGraceSeconds)*time.Second {
			retry = true
			continue
		}
		if attempted >= api.InstanceDivergenceTickLimit {
			retry = true
			break
		}
		attempted++
		if os.Getenv(NodeInventoryEnforceEnv) != "1" {
			if e.ops != nil {
				e.ops.InstanceDivergence("suppressed").Inc()
			}
			e.log.Warn("sched: missing VM inventory (report-only)", "instance_id", ins.ID, "node_id", nodeID)
			continue
		}
		fingerprint, sampledAt := observation.fingerprint, inventory.SampledAt
		failed, again := e.failMissingRunning(checkCtx, reconciler, ins, nodeID, "missing_from_vmmd_presence", terminalAt,
			func(fresh state.Instance, freshNow time.Time) bool {
				return !fresh.StartedAt.After(sampledAt) &&
					(fresh.StartedAt.IsZero() || freshNow.Sub(fresh.StartedAt) >= time.Duration(api.InstanceDivergenceGraceSeconds)*time.Second) &&
					e.nodePresence.current(nodeID, fingerprint, freshNow)
			})
		if failed {
			reconciled++
		}
		retry = retry || again
	}
	for _, id := range frozenGone {
		if err := e.ParkFrozen(checkCtx, id, "node_restart"); err != nil {
			retry = true
			e.log.Warn("sched: park frozen instance missing from inventory", "instance_id", id, "node_id", nodeID, "err", err)
		}
	}
	if !retry {
		e.nodePresence.markReconciled(nodeID, observation.fingerprint)
	}
	if reconciled > 0 {
		e.log.Warn("sched: stale-instance reconciliation complete",
			"node_id", nodeID, "reconciled", reconciled)
	}
}

// failMissingRunning cleans up and fails one RUNNING row whose node's vmmd
// no longer holds its VM (ADR-419's inventory, or the vmmd restart adoption
// report). stillMissing re-checks the evidence against the row reread under
// the app lock. It returns whether the row was failed, and whether a later
// pass must look at it again.
func (e *Engine) failMissingRunning(ctx context.Context, reconciler staleInstanceStore, ins state.Instance, nodeID, reason string, terminalAt time.Time, stillMissing func(fresh state.Instance, now time.Time) bool) (failed, retry bool) {
	// Serialize with local lifecycle changes and reread before cleanup.
	release := e.lockApp(ins.AppID)
	app, appErr := e.store.AppByID(ctx, ins.AppID)
	if appErr != nil || !e.ownsApp(app) {
		release()
		return false, appErr != nil && !errors.Is(appErr, state.ErrNotFound)
	}
	fresh, readErr := e.store.InstanceByID(ctx, ins.ID)
	if readErr != nil || fresh.State != string(state.StateRunning) || fresh.NodeID != nodeID ||
		!stillMissing(fresh, e.now()) {
		release()
		return false, (readErr != nil && !errors.Is(readErr, state.ErrNotFound)) ||
			(readErr == nil && fresh.State == string(state.StateRunning) && fresh.NodeID == nodeID)
	}
	// A dead process may retain its Manager entry, netns and allocator
	// lease after a lost exit relay. Clean those before admitting a replacement.
	if e.vmm == nil {
		release()
		e.log.Warn("sched: missing VM cleanup unavailable", "instance_id", ins.ID)
		return false, true
	}
	if err := e.timedDestroy(ctx, nodeID, ins.ID, DestroyTimeout); err != nil {
		release()
		e.log.Warn("sched: missing VM cleanup failed", "instance_id", ins.ID, "err", err)
		return false, true
	}
	err := reconciler.FailRunningInstanceIfOwnedByNode(ctx, ins.ID, nodeID, terminalAt)
	release()
	if errors.Is(err, state.ErrConflict) {
		// A concurrent lifecycle writer already moved the row.
		e.log.Debug("sched: stale-instance reconciliation race",
			"instance_id", ins.ID, "node_id", nodeID)
		return false, false
	}
	if err != nil {
		e.log.Warn("sched: stale-instance reconciliation update failed",
			"instance_id", ins.ID, "node_id", nodeID, "err", err)
		return false, true
	}
	if e.ledger != nil {
		e.ledger.Release(ins.ID)
	}
	subject := ins.ID
	data, _ := json.Marshal(map[string]any{
		"from": "running", "to": "failed",
		"reason":  reason,
		"node_id": nodeID, "ts": terminalAt,
	})
	if eventErr := e.store.AppendEvent(
		ctx, "schedd", "stale_instance_reconciled", &subject, data); eventErr != nil {
		e.log.Warn("sched: stale-instance reconciliation audit failed",
			"instance_id", ins.ID, "err", eventErr)
	}
	e.emitInstanceChanged(ctx, ins.ID, ins.AppID,
		state.StateFailed, ins.WakeID)
	if ins.Mode == string(state.InstanceModeService) {
		e.scheduleServiceReconcile(ctx, ins.DeploymentID)
	} else if ins.Mode == string(state.InstanceModeWorker) {
		e.scheduleWorkerReconcile(ctx, ins.DeploymentID)
	}
	e.log.Warn("sched: failed stale running instance",
		"instance_id", ins.ID, "app_id", ins.AppID,
		"node_id", nodeID, "reason", reason)
	if e.ops != nil {
		e.ops.InstanceDivergence("failed").Inc()
	}
	return true, false
}
