package sched

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const nodePresenceConfirmReports = 2
const nodePresenceRefreshReports = 30

type nodePresenceReport struct {
	fingerprint           string
	liveCount             int32
	consecutive           int
	reconciledFingerprint string
	reconciledAt          int
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

// Observe accepts only complete vmmd reports. A capacity frame whose live_count
// disagrees with its instance rows is intentionally inconclusive: resident
// stats may be unavailable during startup or on a non-Linux development host.
// Identical complete reports are confirmed twice before any database state is
// changed.
func (t *nodePresenceTracker) Observe(nodeID string, liveCount int32, rows []NodeTelemetry) (nodePresenceObservation, bool) {
	if t == nil || nodeID == "" {
		return nodePresenceObservation{}, false
	}
	fingerprint, reported, ok := completeNodePresence(liveCount, rows)
	if !ok {
		t.mu.Lock()
		delete(t.reports, nodeID)
		t.mu.Unlock()
		return nodePresenceObservation{}, false
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reports == nil {
		t.reports = make(map[string]nodePresenceReport)
	}
	report := t.reports[nodeID]
	if report.fingerprint == fingerprint && report.liveCount == liveCount {
		report.consecutive++
	} else {
		report = nodePresenceReport{
			fingerprint: fingerprint,
			liveCount:   liveCount,
			consecutive: 1,
		}
	}
	shouldCheck := report.consecutive >= nodePresenceConfirmReports &&
		(report.reconciledAt == 0 ||
			report.reconciledFingerprint != fingerprint ||
			report.consecutive-report.reconciledAt >= nodePresenceRefreshReports)
	t.reports[nodeID] = report
	if !shouldCheck {
		return nodePresenceObservation{}, false
	}
	return nodePresenceObservation{
		fingerprint: fingerprint,
		reported:    reported,
		shouldCheck: true,
	}, true
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

func completeNodePresence(liveCount int32, rows []NodeTelemetry) (string, map[string]struct{}, bool) {
	if liveCount < 0 || int(liveCount) != len(rows) {
		return "", nil, false
	}
	reported := make(map[string]struct{}, len(rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.InstanceID == "" {
			return "", nil, false
		}
		if _, duplicate := reported[row.InstanceID]; duplicate {
			return "", nil, false
		}
		reported[row.InstanceID] = struct{}{}
		ids = append(ids, row.InstanceID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ","), reported, true
}

type staleInstanceStore interface {
	FailRunningInstanceIfOwnedByNode(ctx context.Context, id, nodeID string, terminalAt time.Time) error
}

// ObserveNodeInstances reconciles RUNNING database rows that disappear from
// two consecutive complete vmmd presence reports. This closes the restart
// gap: a healthy compute node can have vmmd restarted, leaving the heartbeat
// healthy while its in-memory VM map and all Firecracker processes are gone.
//
// The method is deliberately fail-open for capacity reporting. A transient
// database error leaves the observation unmarked, so the next matching report
// retries the check; it never turns a telemetry problem into a stream failure.
func (e *Engine) ObserveNodeInstances(ctx context.Context, nodeID string, liveCount int32, rows []NodeTelemetry) {
	if e == nil || e.store == nil || e.nodePresence == nil {
		return
	}
	observation, ok := e.nodePresence.Observe(nodeID, liveCount, rows)
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

	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	instances, err := e.store.ListInstancesByNodeID(checkCtx, nodeID)
	if err != nil {
		e.log.Warn("sched: stale-instance reconciliation list failed",
			"node_id", nodeID, "err", err)
		return
	}

	terminalAt := time.Now().UTC()
	reconciled := 0
	for _, ins := range instances {
		if ins.State != string(state.StateRunning) {
			continue
		}
		if _, present := observation.reported[ins.ID]; present {
			continue
		}
		err := reconciler.FailRunningInstanceIfOwnedByNode(
			checkCtx, ins.ID, nodeID, terminalAt)
		if err == nil {
			if e.ledger != nil {
				e.ledger.Release(ins.ID)
			}
			subject := ins.ID
			data, _ := json.Marshal(map[string]any{
				"from": "running", "to": "failed",
				"reason":  "missing_from_vmmd_presence",
				"node_id": nodeID, "ts": terminalAt,
			})
			if eventErr := e.store.AppendEvent(
				checkCtx, "schedd", "stale_instance_reconciled", &subject, data); eventErr != nil {
				e.log.Warn("sched: stale-instance reconciliation audit failed",
					"instance_id", ins.ID, "err", eventErr)
			}
			e.emitInstanceChanged(checkCtx, ins.ID, ins.AppID,
				state.StateFailed, ins.WakeID)
			if ins.Mode == string(state.InstanceModeService) {
				e.scheduleServiceReconcile(checkCtx, ins.DeploymentID)
			}
			e.log.Warn("sched: failed stale running instance",
				"instance_id", ins.ID, "app_id", ins.AppID,
				"node_id", nodeID)
			reconciled++
			continue
		}
		if errors.Is(err, state.ErrConflict) {
			// A concurrent lifecycle writer already moved the row.
			e.log.Debug("sched: stale-instance reconciliation race",
				"instance_id", ins.ID, "node_id", nodeID)
			continue
		}
		e.log.Warn("sched: stale-instance reconciliation update failed",
			"instance_id", ins.ID, "node_id", nodeID, "err", err)
	}
	e.nodePresence.markReconciled(nodeID, observation.fingerprint)
	if reconciled > 0 {
		e.log.Warn("sched: stale-instance reconciliation complete",
			"node_id", nodeID, "reconciled", reconciled)
	}
}
