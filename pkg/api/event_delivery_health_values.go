package api

import (
	"strings"
	"time"
)

func deliveryHealthValue(h EventConsumerDeliveryHealth, metric string) (float64, bool, time.Time) {
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
