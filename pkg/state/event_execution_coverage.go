package state

import (
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type executionCoverage struct {
	controlReceipts         map[string]map[string]api.EventExecutionLineageRetryJobControlReceipt
	jobs                    map[string]api.EventExecutionLineageRetryJob
	cases                   map[int64]api.EventExecutionLineageCase
	retries                 map[string]lineageCaseRetry
	repair                  lineageRepairProgress
	reconciliationRequested *time.Time
	ledgerStarted           time.Time
	ledgerBaseline          bool
	ledgerLostThrough       *time.Time
	app                     string
	started                 time.Time
	baseline                bool
	lost                    *time.Time
	reason                  string
}

func applyExecutionCoverage(out *api.EventConsumerExecutionHealth, c executionCoverage, expired bool) {
	out.CoverageStartedAt = &c.started
	if c.lost != nil {
		at := *c.lost
		out.CoverageLostAt = &at
	}
	out.HistoryStatus, out.HistoryReason = "unknown", "baseline_unknown"
	switch {
	case c.lost != nil:
		out.HistoryStatus, out.HistoryReason = "partial", c.reason
	case !c.baseline:
	case out.WindowStart.Before(c.started):
		out.HistoryReason = "window_precedes_collection"
	case expired:
		out.HistoryStatus, out.HistoryReason = "partial", "attempt_retention_expired"
	case out.Truncated:
		out.HistoryStatus, out.HistoryReason = "partial", "history_truncated"
	case out.MissingRoots > 0:
		out.HistoryStatus, out.HistoryReason = "partial", "missing_roots"
	case out.Unknown > 0 || out.UnknownAttempts > 0:
		out.HistoryStatus, out.HistoryReason = "partial", "unknown_outcomes"
	default:
		out.HistoryStatus, out.HistoryReason = "complete", "window_evidence_complete"
		out.HistoryComplete = true
	}
}

func (m *MemStore) loseExecutionCoverageLocked(app, reason string, now time.Time) {
	for id, c := range m.eventExecutionCoverage {
		if c.lost != nil || !sameMemUUID(c.app, app) {
			continue
		}
		at := now
		c.lost, c.reason = &at, reason
		m.eventExecutionCoverage[id] = c
	}
}

func equalCoverageTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func (m *MemStore) loseSubscriptionExecutionCoverageLocked(sub, reason string, now time.Time) {
	id := canonicalMemUUID(sub)
	c, ok := m.eventExecutionCoverage[id]
	if !ok || c.lost != nil {
		return
	}
	at := now
	c.lost, c.reason = &at, reason
	m.eventExecutionCoverage[id] = c
}
