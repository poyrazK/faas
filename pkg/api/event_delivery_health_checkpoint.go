package api

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// EventDeliveryHealthCheckpoint stores historical evidence, not live confirmation.
type EventDeliveryHealthCheckpoint struct {
	Version            int                            `json:"version"`
	App                string                         `json:"app"`
	AppID              string                         `json:"app_id"`
	SubscriptionID     string                         `json:"subscription_id"`
	Window             string                         `json:"window"`
	Session            string                         `json:"session"`
	Sequence           uint64                         `json:"sequence"`
	Conditions         []EventDeliveryHealthCondition `json:"conditions"`
	Pending            []EventDeliveryHealthAlert     `json:"pending"`
	AcknowledgedAlerts []EventDeliveryHealthAlert     `json:"acknowledged_alerts,omitempty"`
}

type EventDeliveryHealthAcknowledgement struct {
	IncidentID string `json:"incident_id"`
	Sequence   uint64 `json:"sequence"`
}

func deliveryHealthIncidentSequence(id, session string) (uint64, bool) {
	raw, ok := strings.CutPrefix(id, session+"-")
	n, err := strconv.ParseUint(raw, 10, 64)
	return n, ok && err == nil && n > 0 && n <= EventDeliveryHealthAlertSequenceMax && strconv.FormatUint(n, 10) == raw
}

func deliveryHealthMetricThreshold(metric string, threshold float64) bool {
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold <= 0 {
		return false
	}
	if strings.HasPrefix(metric, "slo.") {
		switch metric {
		case "slo.routing_latency_p95_seconds", "slo.completion_latency_p95_seconds":
			return threshold >= EventDeliveryHealthWatchThresholdMin.Seconds() && threshold <= EventDeliveryHealthWatchThresholdMax.Seconds()
		case "slo.retry_rate_per_second", "slo.dead_letter_rate_per_second":
			return threshold <= float64(EventDeliveryHealthWatchCountMax)
		case "slo.routing_failure_pct":
			return threshold >= EventDeliveryHealthSLOPercentageMin && threshold <= EventDeliveryHealthSLOPercentageMax
		default:
			return false
		}
	}
	count := metric == "retrying_executions" || metric == "dead_lettered_executions"
	switch metric {
	case "oldest_pending_seconds", "paused_seconds", "circuit_blocked_seconds", "capacity_blocked_seconds", "retrying_executions", "dead_lettered_executions":
	default:
		parts := strings.Split(metric, ".")
		if len(parts) != 3 || parts[0] != "waiting_reason" {
			return false
		}
		if _, ok := NewEventPendingWaitingReasonCounts()[parts[1]]; !ok {
			return false
		}
		if parts[2] != "seconds" && parts[2] != "recipients" {
			return false
		}
		count = parts[2] == "recipients"
	}
	if count {
		return threshold <= float64(EventDeliveryHealthWatchCountMax) && math.Trunc(threshold) == threshold
	}
	return threshold >= EventDeliveryHealthWatchThresholdMin.Seconds() && threshold <= EventDeliveryHealthWatchThresholdMax.Seconds()
}

// ValidateEventDeliveryHealthCheckpoint validates bounded historical alert state.
func ValidateEventDeliveryHealthCheckpoint(saved EventDeliveryHealthCheckpoint) error {
	if saved.Version != 1 || saved.App == "" || saved.SubscriptionID == "" || (saved.AppID == "" && (len(saved.Pending) > 0 || len(saved.AcknowledgedAlerts) > 0)) || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(saved.Session) || saved.Sequence > EventDeliveryHealthAlertSequenceMax || len(saved.Pending) > EventDeliveryHealthAlertQueueMax || len(saved.AcknowledgedAlerts) > EventDeliveryHealthAcknowledgementHistoryMax {
		return ErrValidation("invalid delivery health checkpoint bounds")
	}
	if _, err := EventConsumerHealthWindow(saved.Window); err != nil {
		return ErrValidation("invalid checkpoint window")
	}
	metrics := map[string]EventDeliveryHealthCondition{}
	active := map[string]bool{}
	for _, c := range saved.Conditions {
		if _, exists := metrics[c.Metric]; exists || !deliveryHealthMetricThreshold(c.Metric, c.Threshold) || math.IsNaN(c.Value) || math.IsInf(c.Value, 0) || c.Value < 0 {
			return ErrValidation("invalid checkpoint condition")
		}
		if strings.HasPrefix(c.Metric, "slo.") {
			if c.MinimumSamples < 1 || c.MinimumSamples > EventDeliveryHealthWatchCountMax || !finiteSLOValue(c.SustainSeconds) || c.SustainSeconds < EventDeliveryHealthWatchThresholdMin.Seconds() || c.SustainSeconds > EventDeliveryHealthWatchThresholdMax.Seconds() {
				return ErrValidation("invalid checkpoint SLO configuration")
			}
		} else if c.MinimumSamples != 0 || c.SustainSeconds != 0 {
			return ErrValidation("unexpected checkpoint SLO configuration")
		}
		if c.State != "clear" && c.State != "unknown" && c.State != "firing" {
			return ErrValidation("invalid checkpoint condition state")
		}
		if c.IncidentID != "" {
			n, ok := deliveryHealthIncidentSequence(c.IncidentID, saved.Session)
			if !ok || n > saved.Sequence || active[c.IncidentID] || c.State == "clear" || c.ObservedAt.IsZero() || saved.AppID == "" {
				return ErrValidation("invalid checkpoint incident")
			}
			active[c.IncidentID] = true
		} else if c.State == "firing" {
			return ErrValidation("missing checkpoint incident")
		}
		if c.State == "firing" && c.Value < c.Threshold {
			return ErrValidation("checkpoint firing value below threshold")
		}
		metrics[c.Metric] = c
	}
	if len(metrics) == 0 || len(saved.Pending)+len(active) > EventDeliveryHealthAlertQueueMax || uint64(len(active)) > EventDeliveryHealthAlertSequenceMax-saved.Sequence {
		return ErrValidation("invalid checkpoint reservations")
	}
	var lastPending uint64
	for _, a := range saved.Pending {
		if a.Sequence <= lastPending {
			return ErrValidation("unordered checkpoint pending alerts")
		}
		lastPending = a.Sequence
	}
	history := append([]EventDeliveryHealthAlert{}, saved.Pending...)
	history = append(history, saved.AcknowledgedAlerts...)
	sort.Slice(history, func(i, j int) bool { return history[i].Sequence < history[j].Sequence })
	claims := map[uint64]string{}
	lineage := map[string]string{}
	closed := map[string]bool{}
	var last uint64
	for _, a := range history {
		c, known := metrics[a.Metric]
		n, ok := deliveryHealthIncidentSequence(a.IncidentID, saved.Session)
		if !known || !ok || n > a.Sequence || a.Sequence <= last || a.Sequence > saved.Sequence || a.AppID != saved.AppID || a.SubscriptionID != saved.SubscriptionID || a.At.IsZero() || a.Threshold != c.Threshold || a.MinimumSamples != c.MinimumSamples || a.SustainSeconds != c.SustainSeconds || math.IsNaN(a.Value) || math.IsInf(a.Value, 0) || a.Value < 0 || closed[a.IncidentID] || (lineage[a.IncidentID] != "" && lineage[a.IncidentID] != a.Metric) {
			return ErrValidation("invalid checkpoint pending alert")
		}
		switch a.Transition {
		case "opened":
			if n != a.Sequence || a.Reason != "threshold_breached" || a.Value < a.Threshold {
				return ErrValidation("invalid checkpoint opening")
			}
		case "resolved":
			if n >= a.Sequence || a.Reason != "threshold_cleared" || a.Value >= a.Threshold {
				return ErrValidation("invalid checkpoint resolution")
			}
			closed[a.IncidentID] = true
		case "confirmation_lost":
			if n >= a.Sequence || (a.Reason != "sample_gap" && a.Reason != "evidence_unavailable") {
				return ErrValidation("invalid checkpoint confirmation loss")
			}
			closed[a.IncidentID] = true
		default:
			return ErrValidation("unknown checkpoint transition")
		}
		claims[a.Sequence] = a.IncidentID
		lineage[a.IncidentID] = a.Metric
		last = a.Sequence
	}
	for _, a := range history {
		n, _ := deliveryHealthIncidentSequence(a.IncidentID, saved.Session)
		if claims[n] != "" && claims[n] != a.IncidentID {
			return ErrValidation("checkpoint pending opening sequence collision")
		}
	}
	for _, c := range saved.Conditions {
		n, _ := deliveryHealthIncidentSequence(c.IncidentID, saved.Session)
		if c.IncidentID != "" && claims[n] != "" && claims[n] != c.IncidentID {
			return ErrValidation("checkpoint incident opening sequence collision")
		}
		if c.IncidentID != "" && (closed[c.IncidentID] || (lineage[c.IncidentID] != "" && lineage[c.IncidentID] != c.Metric)) {
			return ErrValidation("inconsistent checkpoint active incident")
		}
	}
	return nil
}

// AcknowledgeEventDeliveryHealthAlerts removes exact pending transitions atomically.
// Repeated exact pairs are idempotent within the bounded acknowledgement history.
// Unknown pairs fail without changing the input.
func AcknowledgeEventDeliveryHealthAlerts(saved EventDeliveryHealthCheckpoint, acks []EventDeliveryHealthAcknowledgement) (EventDeliveryHealthCheckpoint, error) {
	if err := ValidateEventDeliveryHealthCheckpoint(saved); err != nil {
		return saved, err
	}
	if len(acks) > EventDeliveryHealthAlertQueueMax {
		return saved, ErrValidation("too many alert acknowledgements")
	}
	known := map[EventDeliveryHealthAcknowledgement]bool{}
	for _, a := range append(append([]EventDeliveryHealthAlert{}, saved.Pending...), saved.AcknowledgedAlerts...) {
		known[EventDeliveryHealthAcknowledgement{a.IncidentID, a.Sequence}] = true
	}
	selected := map[EventDeliveryHealthAcknowledgement]bool{}
	for _, a := range acks {
		if !known[a] {
			return saved, ErrValidation("acknowledgement must match a pending or recently acknowledged transition")
		}
		selected[a] = true
	}
	acknowledged := append([]EventDeliveryHealthAlert{}, saved.AcknowledgedAlerts...)
	pending := make([]EventDeliveryHealthAlert, 0, len(saved.Pending))
	for _, a := range saved.Pending {
		if !selected[EventDeliveryHealthAcknowledgement{a.IncidentID, a.Sequence}] {
			pending = append(pending, a)
		} else {
			acknowledged = append(acknowledged, a)
		}
	}
	if len(acknowledged) > EventDeliveryHealthAcknowledgementHistoryMax {
		acknowledged = acknowledged[len(acknowledged)-EventDeliveryHealthAcknowledgementHistoryMax:]
	}
	saved.AcknowledgedAlerts = acknowledged
	saved.Pending = pending
	return saved, nil
}

func restoreDeliveryHealthCheckpoint(out *EventDeliveryHealthWatchResult, conditions []*deliveryHealthCondition, app string, o EventDeliveryHealthWatchOptions) error {
	if o.Resume == nil {
		return nil
	}
	if !o.DurableAlerts {
		return ErrValidation("resuming requires durable alerts")
	}
	saved := o.Resume
	if err := ValidateEventDeliveryHealthCheckpoint(*saved); err != nil {
		return err
	}
	window, _ := EventConsumerHealthWindow(o.Window)
	savedWindow, _ := EventConsumerHealthWindow(saved.Window)
	if saved.App != app || !strings.EqualFold(saved.SubscriptionID, out.SubscriptionID) || window != savedWindow || len(saved.Conditions) != len(conditions) {
		return ErrValidation("checkpoint watch selection mismatch")
	}
	for i, c := range conditions {
		previous := saved.Conditions[i]
		if c.Metric != previous.Metric || c.Threshold != previous.Threshold || c.MinimumSamples != previous.MinimumSamples || c.SustainSeconds != previous.SustainSeconds {
			return ErrValidation("checkpoint thresholds mismatch")
		}
		c.observedAt = previous.ObservedAt
		c.Value = previous.Value
		if previous.IncidentID != "" {
			c.IncidentID = previous.IncidentID
			c.State = "unknown"
			c.resuming = true
			out.reserved++
		}
	}
	out.AppID = saved.AppID
	out.Session = saved.Session
	out.Sequence = saved.Sequence
	out.Checkpoint.Pending = append([]EventDeliveryHealthAlert{}, saved.Pending...)
	out.Checkpoint.AcknowledgedAlerts = append([]EventDeliveryHealthAlert{}, saved.AcknowledgedAlerts...)
	return nil
}

func deliveryHealthCanOpen(out *EventDeliveryHealthWatchResult) bool {
	if out.Checkpoint == nil {
		return true
	}
	if out.queueFull {
		return false
	}
	if !deliveryHealthOpeningCapacity(out) {
		out.queueFull = true
		return false
	}
	return true
}

func deliveryHealthQueueError(out EventDeliveryHealthWatchResult) error {
	return fmt.Errorf("delivery health alert outbox full at sequence %d; acknowledge pending notifications before resuming", out.Sequence)
}

func deliveryHealthOpeningCapacity(out *EventDeliveryHealthWatchResult) bool {
	return out.Checkpoint == nil || (len(out.Checkpoint.Pending)+out.reserved+2 <= EventDeliveryHealthAlertQueueMax && out.Sequence+uint64(out.reserved)+2 <= EventDeliveryHealthAlertSequenceMax)
}
