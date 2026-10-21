package api

import (
	"strings"
	"time"
)

func deliveryHealthValue(h EventConsumerDeliveryHealth, metric string) (float64, bool, time.Time) {
	if strings.HasPrefix(metric, "processing_") {
		return deliveryHealthProcessingValue(h, metric)
	}
	if metric == "capacity_command_receipt_budget_used_slots" {
		_, valid, observed := deliveryHealthReceiptBudgetValue(h)
		if !valid {
			return 0, false, observed
		}
		return float64(h.CapacityPressure.CapacityCommandBudget.RetainedReceipts), true, observed
	}
	if metric == "capacity_command_receipt_budget_full_seconds" {
		return deliveryHealthReceiptBudgetValue(h)
	}
	if strings.HasPrefix(metric, "lineage_retry_") {
		return deliveryHealthLineageRetryValue(h, metric)
	}
	if strings.HasPrefix(metric, "waiting_reason.") {
		return deliveryHealthWaitingReasonValue(h, metric)
	}
	r, e := h.Routing, h.Execution
	if metric != "retrying_executions" && metric != "dead_lettered_executions" && metric != "capacity_blocked_seconds" && r.Coverage != "bounded_recorded_outcomes" {
		return 0, false, r.ObservedAt
	}
	switch metric {
	case "oldest_pending_seconds":
		if r.PendingRecipients < 0 {
			return 0, false, r.ObservedAt
		}
		if r.PendingRecipients == 0 {
			return 0, r.OldestPendingAt == nil, r.ObservedAt
		}
		if r.OldestPendingAt == nil || r.OldestPendingAt.IsZero() || r.OldestPendingAt.After(r.ObservedAt) {
			return 0, false, r.ObservedAt
		}
		return r.ObservedAt.Sub(*r.OldestPendingAt).Seconds(), true, r.ObservedAt
	case "paused_seconds":
		if !r.Paused {
			return 0, r.PausedSeconds == 0, r.ObservedAt
		}
		return r.PausedSeconds, true, r.ObservedAt
	case "retrying_executions", "dead_lettered_executions":
		valid := e.Coverage == "bounded_retained_execution_roots" && !e.Truncated && e.MissingRoots == 0 && e.Unknown == 0 && e.RetainedRoots > 0
		if metric == "retrying_executions" {
			return float64(e.Retrying), valid, e.ObservedAt
		}
		return float64(e.DeadLettered), valid, e.ObservedAt
	case "circuit_blocked_seconds":
		circuit := r.CircuitBreaker
		if circuit == nil {
			return 0, true, r.ObservedAt
		}
		if !strings.EqualFold(circuit.SubscriptionID, r.SubscriptionID) || circuit.HistoryIncomplete {
			return 0, false, r.ObservedAt
		}
		switch circuit.State {
		case "disabled":
			return 0, !circuit.Enabled, r.ObservedAt
		case "closed":
			return 0, circuit.Enabled, r.ObservedAt
		case "open", "half_open", "draining":
			return 1, circuit.Enabled, r.ObservedAt
		}
		return 0, false, r.ObservedAt
	case "capacity_blocked_seconds":
		return deliveryHealthCapacityValue(h)
	}
	return 0, false, time.Time{}
}

func deliveryHealthCapacityValue(h EventConsumerDeliveryHealth) (float64, bool, time.Time) {
	counts := h.CapacityWaits
	if counts == nil {
		return 0, false, time.Time{}
	}
	valid := counts.Complete && counts.Coverage == "all_pending_application_recipients" && strings.EqualFold(counts.AppID, h.Routing.AppID) && strings.EqualFold(counts.SubscriptionID, h.Routing.SubscriptionID) && counts.PendingRecipients >= 0 && counts.Consumer >= 0 && counts.App >= 0 && counts.Account >= 0
	// Subtraction avoids overflow and verifies the mutually exclusive scopes.
	remaining := counts.PendingRecipients
	for _, count := range []int64{counts.Consumer, counts.App, counts.Account} {
		if count < 0 || count > remaining {
			valid = false
			break
		}
		remaining -= count
	}
	return float64(counts.PendingRecipients - remaining), valid, counts.ObservedAt
}

func deliveryHealthWaitingReasonValue(h EventConsumerDeliveryHealth, metric string) (float64, bool, time.Time) {
	counts := h.WaitingReasons
	if counts == nil {
		return 0, false, time.Time{}
	}
	parts := strings.Split(metric, ".")
	if len(parts) != 3 || (parts[2] != "recipients" && parts[2] != "seconds") {
		return 0, false, counts.ObservedAt
	}
	n, known := counts.Counts[parts[1]]
	valid := known && counts.Complete && counts.Coverage == "all_pending_application_recipients" && strings.EqualFold(counts.AppID, h.Routing.AppID) && strings.EqualFold(counts.SubscriptionID, h.Routing.SubscriptionID) && !counts.WindowAt.IsZero() && !counts.WindowAt.After(counts.ObservedAt) && EventPendingWaitingReasonCountsComplete(counts.Counts, counts.PendingRecipients)
	if parts[2] == "seconds" {
		if n > 0 {
			return 1, valid, counts.ObservedAt
		}
		return 0, valid, counts.ObservedAt
	}
	return float64(n), valid && n <= EventDeliveryHealthWatchCountMax, counts.ObservedAt
}

// Retry-job evidence is independent of execution-root history and the outcome window.
func deliveryHealthLineageRetryValue(h EventConsumerDeliveryHealth, metric string) (float64, bool, time.Time) {
	e := h.Execution
	jobs := e.LineageRetryJobs
	if jobs == nil {
		return 0, false, e.ObservedAt
	}
	valid := jobs.CountsComplete && jobs.Coverage == "bounded_retained_active_lineage_retry_jobs" &&
		jobs.JobLimit > 0 && int64(jobs.JobLimit) <= EventDeliveryHealthWatchCountMax &&
		jobs.InactivityWarningSeconds > 0 && jobs.RetentionWarningSeconds > 0
	// Validate nonnegative bounded partitions without addition overflow.
	for _, n := range []int64{jobs.ObservedJobs, jobs.PendingJobs, jobs.PausedJobs, jobs.RemainingCases, jobs.PendingRemainingCases, jobs.PausedRemainingCases, jobs.StalledPendingJobs, jobs.RetentionRiskJobs, jobs.UnknownProgressJobs} {
		valid = valid && n >= 0 && n <= EventDeliveryHealthWatchCountMax
	}
	valid = valid && jobs.ObservedJobs <= int64(jobs.JobLimit) &&
		jobs.PendingJobs <= jobs.ObservedJobs && jobs.PausedJobs == jobs.ObservedJobs-jobs.PendingJobs &&
		jobs.PendingRemainingCases <= jobs.RemainingCases && jobs.PausedRemainingCases == jobs.RemainingCases-jobs.PendingRemainingCases &&
		jobs.StalledPendingJobs <= jobs.PendingJobs && jobs.RetentionRiskJobs <= jobs.ObservedJobs && jobs.UnknownProgressJobs <= jobs.ObservedJobs
	valid = valid && jobs.PendingRemainingCases >= jobs.PendingJobs && jobs.PausedRemainingCases >= jobs.PausedJobs &&
		(jobs.PendingJobs != 0 || jobs.PendingRemainingCases == 0) && (jobs.PausedJobs != 0 || jobs.PausedRemainingCases == 0)
	switch metric {
	case "lineage_retry_stalled_jobs":
		return float64(jobs.StalledPendingJobs), valid, e.ObservedAt
	case "lineage_retry_retention_risk_jobs":
		return float64(jobs.RetentionRiskJobs), valid, e.ObservedAt
	case "lineage_retry_paused_seconds":
		if jobs.PausedJobs > 0 {
			return 1, valid, e.ObservedAt
		}
		return 0, valid, e.ObservedAt
	}
	return 0, false, e.ObservedAt
}

// Receipt budget evidence is independent of routing and execution history coverage.
func deliveryHealthReceiptBudgetValue(h EventConsumerDeliveryHealth) (float64, bool, time.Time) {
	pressure := h.CapacityPressure
	if pressure == nil || pressure.CapacityCommandBudget == nil {
		return 0, false, time.Time{}
	}
	b := pressure.CapacityCommandBudget
	valid := pressure.Complete && pressure.Coverage == "live_slots_and_all_pending_application_recipients" && b.Complete &&
		b.AppID != "" && b.SubscriptionID != "" && strings.EqualFold(pressure.AppID, h.Routing.AppID) && strings.EqualFold(pressure.SubscriptionID, h.Routing.SubscriptionID) &&
		strings.EqualFold(b.AppID, pressure.AppID) && strings.EqualFold(b.SubscriptionID, pressure.SubscriptionID) && b.ObservedAt.Equal(pressure.ObservedAt) &&
		b.Limit == EventSubscriptionCapacityCommandMaxReceipts && b.RetainedReceipts >= 0 && b.RetainedReceipts <= b.Limit &&
		b.RemainingSlots == b.Limit-b.RetainedReceipts && b.Full == (b.RemainingSlots == 0) &&
		((b.RetainedReceipts == 0) == (b.EarliestExpiryAt == nil)) && (b.EarliestExpiryAt == nil || b.EarliestExpiryAt.After(b.ObservedAt))
	if b.Full {
		return 1, valid, b.ObservedAt
	}
	return 0, valid, b.ObservedAt
}

func deliveryHealthProcessingValue(h EventConsumerDeliveryHealth, metric string) (float64, bool, time.Time) {
	p := h.Processing
	if p == nil {
		return 0, false, time.Time{}
	}
	valid := p.Complete && p.Coverage == "retained_deferred_invocations" && p.AppID == h.Routing.AppID && p.SubscriptionID == h.Routing.SubscriptionID && p.Processing >= 0 && p.Expired >= 0 && p.NearExpiry >= 0 && p.NearExpiry <= p.Processing && p.NearExpirySeconds == int(EventProcessingNearExpiry/time.Second)
	if !valid {
		return 0, false, p.ObservedAt
	}
	switch metric {
	case "processing_lease_risk_seconds":
		return float64(p.NearExpiry), true, p.ObservedAt
	case "processing_expired_seconds":
		return float64(p.Expired), true, p.ObservedAt
	case "processing_oldest_seconds":
		if p.Processing == 0 {
			return 0, p.OldestProcessingOfferedAt == nil, p.ObservedAt
		}
		if p.OldestProcessingOfferedAt == nil || p.OldestProcessingOfferedAt.IsZero() || p.OldestProcessingOfferedAt.After(p.ObservedAt) {
			return 0, false, p.ObservedAt
		}
		return p.ObservedAt.Sub(*p.OldestProcessingOfferedAt).Seconds(), true, p.ObservedAt
	}
	return 0, false, p.ObservedAt
}
