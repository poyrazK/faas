package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type executionLedgerStats struct {
	started, lost, expired, retain              *time.Time
	known                                       bool
	success, failed, unknown, dead, completions int64
	p95                                         float64
}

func applyExecutionLedgerStats(out *api.EventConsumerExecutionHealth, s executionLedgerStats) {
	out.WindowCoverage = "durable_execution_outcomes"
	out.WindowHistoryComplete = false
	out.WindowHistoryStatus, out.WindowHistoryReason = "unknown", "ledger_baseline_unknown"
	out.WindowCoverageStartedAt = cloneEventReceiptTime(s.started)
	out.WindowCoverageLostThroughAt = cloneEventReceiptTime(s.lost)
	if s.expired != nil && (out.WindowCoverageLostThroughAt == nil || s.expired.After(*out.WindowCoverageLostThroughAt)) {
		out.WindowCoverageLostThroughAt = cloneEventReceiptTime(s.expired)
	}
	out.WindowEvidenceRetainUntil = cloneEventReceiptTime(s.retain)
	out.WindowEvidenceRetentionSeconds = int64(api.EventExecutionLedgerRetention / time.Second)
	out.SuccessfulAttempts, out.FailedAttempts, out.UnknownAttempts = s.success, s.failed, s.unknown
	out.WindowCompletions, out.WindowDeadLetters = s.completions, s.dead
	out.CompletionLatencyP95Seconds = s.p95
	out.HandlerFailurePct = 0
	if n := s.success + s.failed; n > 0 {
		out.HandlerFailurePct = 100 * float64(s.failed) / float64(n)
	}
	out.DeadLetterRatePerSecond = float64(s.dead) / out.ObservedAt.Sub(out.WindowStart).Seconds()
	switch {
	case s.lost != nil && !out.WindowStart.After(*s.lost):
		out.WindowHistoryStatus, out.WindowHistoryReason = "partial", "ledger_history_gap"
	case s.expired != nil:
		out.WindowHistoryStatus, out.WindowHistoryReason = "partial", "ledger_retention_expired"
	case !s.known || s.started == nil:
	case out.WindowStart.Before(*s.started):
		out.WindowHistoryReason = "window_precedes_ledger_collection"
	case s.unknown > 0:
		out.WindowHistoryStatus, out.WindowHistoryReason = "partial", "unknown_outcomes"
	default:
		out.WindowHistoryStatus, out.WindowHistoryReason = "complete", "window_evidence_complete"
		out.WindowHistoryComplete = true
	}
	if !out.WindowHistoryComplete && out.HistoryComplete {
		out.HistoryComplete = false
		out.HistoryStatus, out.HistoryReason = out.WindowHistoryStatus, out.WindowHistoryReason
	}
}
func (m *MemStore) observeEventExecutionLedgerWindowLocked(out *api.EventConsumerExecutionHealth, account string) {
	s := executionLedgerStats{}
	if c, ok := m.eventExecutionCoverage[out.SubscriptionID]; ok {
		if !c.ledgerStarted.IsZero() {
			s.started = cloneEventReceiptTime(&c.ledgerStarted)
		}
		s.known, s.lost = c.ledgerBaseline, c.ledgerLostThrough
	}
	latencies := []float64{}
	for key, o := range m.eventExecutionOutcomes {
		if !sameMemUUID(o.root.account, account) || !sameMemUUID(o.root.app, out.AppID) || !sameMemUUID(o.root.subscription, out.SubscriptionID) || o.finished.Before(out.WindowStart) || o.finished.After(out.ObservedAt) {
			continue
		}
		if !o.retainUntil.After(out.ObservedAt) {
			if s.expired == nil || o.finished.After(*s.expired) {
				s.expired = cloneEventReceiptTime(&o.finished)
			}
			continue
		}
		if s.retain == nil || o.retainUntil.Before(*s.retain) {
			s.retain = cloneEventReceiptTime(&o.retainUntil)
		}
		if key.kind == "completion" {
			s.completions++
			latencies = append(latencies, o.latency)
			continue
		}
		switch o.outcome {
		case "succeeded":
			s.success++
		case "retry", "failed", "dead_letter":
			s.failed++
			if o.outcome == "dead_letter" {
				s.dead++
			}
		case "unknown":
			s.unknown++
		}
	}
	sort.Float64s(latencies)
	if n := len(latencies); n > 0 {
		p := float64(n-1) * 0.95
		lo := int(p)
		s.p95 = latencies[lo]
		if lo+1 < n {
			s.p95 += (latencies[lo+1] - latencies[lo]) * (p - float64(lo))
		}
	}
	applyExecutionLedgerStats(out, s)
}
