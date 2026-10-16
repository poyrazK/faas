package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// EventDeliveryHealthWatchOptions selects opt-in conditions. Zero disables a
// threshold. Circuit/capacity durations measure consecutive local fresh reads.
type EventDeliveryHealthWatchOptions struct {
	RateRisk *EventDeliveryRateRiskOptions
	// Nil RecordingSince uses verified server inventory; a timestamp is an operator override.
	ProcessingMetricsRecordingSince                               *time.Time
	ProcessingRenewalRejectionFraction, ProcessingLeaseExpiryRate float64
	ProcessingRenewalMinimumSamples                               int64
	ProcessingWindowSustain                                       time.Duration
	ProcessingOldest, ProcessingLeaseRisk, ProcessingExpired      time.Duration
	Window                                                        string
	Interval, Timeout, MaxAge                                     time.Duration
	OldestPending, Paused, CircuitBlocked, CapacityBlocked        time.Duration
	Retrying, DeadLetters                                         int64
	LineageStalled, LineageRetentionRisk                          int64
	LineagePaused                                                 time.Duration
	ReceiptBudgetFull                                             time.Duration
	// ReceiptBudgetRemaining warns below this many slots; zero disables it.
	ReceiptBudgetRemaining  int
	ReceiptBudgetLowSustain time.Duration
	// Recovery is opt-in. Its remaining-slot threshold must be at least the warning threshold.
	ReceiptBudgetRecoveryRemaining int
	ReceiptBudgetRecoverySustain   time.Duration
	// WaitingReasonCounts and WaitingReasonDurations select known pending reasons.
	WaitingReasonCounts    map[string]int64
	WaitingReasonDurations map[string]time.Duration
	DurableAlerts          bool
	Resume                 *EventDeliveryHealthCheckpoint
	SLO                    *EventDeliveryHealthSLOOptions
	OnProgress             func(EventDeliveryHealthWatchResult) error
	// PollAcknowledgements runs synchronously between observations with detached state.
	PollAcknowledgements func(context.Context, EventDeliveryHealthCheckpoint) ([]EventDeliveryHealthAcknowledgement, error)
}

type EventDeliveryHealthAlert struct {
	RateConstraints        string     `json:"rate_constraints,omitempty"`
	MetricRecordingSince   *time.Time `json:"metric_recording_since,omitempty"`
	RecoveryThreshold      float64    `json:"recovery_threshold,omitempty"`
	RecoverySustainSeconds float64    `json:"recovery_sustain_seconds,omitempty"`
	AppID                  string     `json:"app_id"`
	SubscriptionID         string     `json:"subscription_id"`
	Sequence               uint64     `json:"sequence"`
	IncidentID             string     `json:"incident_id"`
	Metric                 string     `json:"metric"`
	Transition             string     `json:"transition"`
	Reason                 string     `json:"reason"`
	At                     time.Time  `json:"at"`
	Value                  float64    `json:"value"`
	Threshold              float64    `json:"threshold"`
	MinimumSamples         int64      `json:"minimum_samples,omitempty"`
	SustainSeconds         float64    `json:"sustain_seconds,omitempty"`
}

type EventDeliveryHealthCondition struct {
	MetricRecordingSince   *time.Time `json:"metric_recording_since,omitempty"`
	RecoveryThreshold      float64    `json:"recovery_threshold,omitempty"`
	RecoverySustainSeconds float64    `json:"recovery_sustain_seconds,omitempty"`
	Metric                 string     `json:"metric"`
	State                  string     `json:"state"`
	Value                  float64    `json:"value"`
	Threshold              float64    `json:"threshold"`
	IncidentID             string     `json:"incident_id,omitempty"`
	ObservedAt             time.Time  `json:"observed_at"`
	MinimumSamples         int64      `json:"minimum_samples,omitempty"`
	SustainSeconds         float64    `json:"sustain_seconds,omitempty"`
}

type EventDeliveryHealthWatchResult struct {
	RateEvidence   *EventDeliveryRateEvidence          `json:"rate_evidence,omitempty"`
	Confirmations  []EventDeliveryHealthConfirmation   `json:"confirmations,omitempty"`
	Checkpoint     *EventDeliveryHealthCheckpoint      `json:"checkpoint,omitempty"`
	SLO            []EventDeliveryHealthSLOObservation `json:"slo,omitempty"`
	reserved       int
	queueFull      bool
	AppID          string                         `json:"app_id,omitempty"`
	SubscriptionID string                         `json:"subscription_id"`
	Session        string                         `json:"session"`
	Sequence       uint64                         `json:"sequence"`
	Observations   int                            `json:"observations"`
	StopReason     string                         `json:"stop_reason"`
	Health         *EventConsumerDeliveryHealth   `json:"health,omitempty"`
	Conditions     []EventDeliveryHealthCondition `json:"conditions"`
	Alerts         []EventDeliveryHealthAlert     `json:"alerts"`
}

type deliveryHealthCondition struct {
	EventDeliveryHealthCondition
	first, localLast, observedAt time.Time
	windowEnd                    time.Time
	samples                      int
	sustained                    bool
	resuming                     bool
	recoveryFirst                time.Time
	recoverySamples              int
	confirmationLossReason       string
	confirmationLossAt           time.Time
}

func (o EventDeliveryHealthWatchOptions) validate() error {
	if o.PollAcknowledgements != nil && (!o.DurableAlerts || o.OnProgress == nil) {
		return ErrValidation("live acknowledgements require durable alerts and a progress callback")
	}
	if _, err := EventConsumerHealthWindow(o.Window); err != nil {
		return ErrValidation(err.Error())
	}
	if o.Interval < EventDeliveryHealthWatchIntervalMin || o.Interval > EventDeliveryHealthWatchIntervalMax || o.Timeout <= 0 || o.Timeout > EventDeliveryHealthWatchTimeoutMax || o.MaxAge < EventDeliveryHealthWatchAgeMin || o.MaxAge > EventDeliveryHealthWatchAgeMax {
		return ErrValidation("invalid delivery health watch interval, timeout or max age")
	}
	if err := validateProcessingWindowWatch(o); err != nil {
		return err
	}
	if err := validateRateRiskOptions(o.RateRisk); err != nil {
		return err
	}
	enabled := o.RateRisk != nil || o.ProcessingRenewalRejectionFraction > 0 || o.ProcessingLeaseExpiryRate > 0
	for _, d := range []time.Duration{o.OldestPending, o.Paused, o.CircuitBlocked, o.CapacityBlocked, o.LineagePaused, o.ReceiptBudgetFull, o.ProcessingOldest, o.ProcessingLeaseRisk, o.ProcessingExpired} {
		if d < 0 || d > EventDeliveryHealthWatchThresholdMax || (d > 0 && d < EventDeliveryHealthWatchThresholdMin) {
			return ErrValidation("invalid delivery health duration threshold")
		}
		enabled = enabled || d > 0
	}
	for _, n := range []int64{o.Retrying, o.DeadLetters, o.LineageStalled, o.LineageRetentionRisk} {
		if n < 0 || n > EventDeliveryHealthWatchCountMax {
			return ErrValidation("invalid delivery health count threshold")
		}
		enabled = enabled || n > 0
	}
	if o.ReceiptBudgetRemaining < 0 || o.ReceiptBudgetRemaining > EventSubscriptionCapacityCommandMaxReceipts || o.ReceiptBudgetLowSustain < 0 || o.ReceiptBudgetLowSustain > EventDeliveryHealthWatchThresholdMax || (o.ReceiptBudgetLowSustain > 0 && o.ReceiptBudgetLowSustain < EventDeliveryHealthWatchThresholdMin) || (o.ReceiptBudgetRemaining == 0 && o.ReceiptBudgetLowSustain != 0) {
		return ErrValidation("invalid receipt budget remaining-slot threshold or sustain duration")
	}
	if o.ReceiptBudgetRecoveryRemaining < 0 || o.ReceiptBudgetRecoveryRemaining > EventSubscriptionCapacityCommandMaxReceipts || (o.ReceiptBudgetRecoveryRemaining > 0 && o.ReceiptBudgetRecoveryRemaining < o.ReceiptBudgetRemaining) || o.ReceiptBudgetRecoverySustain < 0 || o.ReceiptBudgetRecoverySustain > EventDeliveryHealthWatchThresholdMax || (o.ReceiptBudgetRecoverySustain > 0 && o.ReceiptBudgetRecoverySustain < EventDeliveryHealthWatchThresholdMin) || (o.ReceiptBudgetRemaining == 0 && (o.ReceiptBudgetRecoveryRemaining != 0 || o.ReceiptBudgetRecoverySustain != 0)) {
		return ErrValidation("invalid receipt budget recovery threshold or sustain duration")
	}
	enabled = enabled || o.ReceiptBudgetRemaining > 0
	known := NewEventPendingWaitingReasonCounts()
	for reason, n := range o.WaitingReasonCounts {
		if _, ok := known[reason]; !ok || n <= 0 || n > EventDeliveryHealthWatchCountMax {
			return ErrValidation("invalid waiting reason count threshold")
		}
		enabled = true
	}
	for reason, d := range o.WaitingReasonDurations {
		if _, ok := known[reason]; !ok || d < EventDeliveryHealthWatchThresholdMin || d > EventDeliveryHealthWatchThresholdMax {
			return ErrValidation("invalid waiting reason duration threshold")
		}
		enabled = true
	}
	if o.SLO != nil {
		if err := o.SLO.validate(); err != nil {
			return err
		}
		enabled = true
	}
	if !enabled {
		return ErrValidation("enable at least one delivery health threshold")
	}
	return nil
}

func newDeliveryHealthConditions(o EventDeliveryHealthWatchOptions) []*deliveryHealthCondition {
	out := newProcessingWindowConditions(o)
	for _, v := range []struct {
		metric    string
		threshold float64
		sustained bool
	}{
		{"oldest_pending_seconds", o.OldestPending.Seconds(), false},
		{"processing_lease_risk_seconds", o.ProcessingLeaseRisk.Seconds(), true},
		{"processing_expired_seconds", o.ProcessingExpired.Seconds(), true},
		{"retrying_executions", float64(o.Retrying), false},
		{"dead_lettered_executions", float64(o.DeadLetters), false},
		{"paused_seconds", o.Paused.Seconds(), false},
		{"circuit_blocked_seconds", o.CircuitBlocked.Seconds(), true},
		{"capacity_blocked_seconds", o.CapacityBlocked.Seconds(), true},
		{"lineage_retry_stalled_jobs", float64(o.LineageStalled), false},
		{"lineage_retry_retention_risk_jobs", float64(o.LineageRetentionRisk), false},
		{"lineage_retry_paused_seconds", o.LineagePaused.Seconds(), true},
		{"capacity_command_receipt_budget_full_seconds", o.ReceiptBudgetFull.Seconds(), true},
	} {
		if v.threshold > 0 {
			out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: v.metric, State: "unknown", Threshold: v.threshold}, sustained: v.sustained})
		}
	}
	if o.ProcessingOldest > 0 {
		out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: "processing_oldest_seconds", State: "unknown", Threshold: o.ProcessingOldest.Seconds(), MinimumSamples: 2, SustainSeconds: EventProcessingAlertSustain.Seconds()}})
	}
	if o.ReceiptBudgetRemaining > 0 {
		sustain := o.ReceiptBudgetLowSustain
		if sustain == 0 {
			sustain = EventSubscriptionCapacityCommandLowSustainDefault
		}
		// Existing lifecycle comparisons are upper-bound breaches. Express remaining
		// slots below N as retained receipts at least limit-N+1, preserving raw usage.
		condition := &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: "capacity_command_receipt_budget_used_slots", State: "unknown", Threshold: float64(EventSubscriptionCapacityCommandMaxReceipts - o.ReceiptBudgetRemaining + 1), MinimumSamples: 2, SustainSeconds: sustain.Seconds()}}
		if o.ReceiptBudgetRecoveryRemaining > 0 || o.ReceiptBudgetRecoverySustain > 0 {
			remaining := o.ReceiptBudgetRecoveryRemaining
			if remaining == 0 {
				remaining = o.ReceiptBudgetRemaining
			}
			recovery := o.ReceiptBudgetRecoverySustain
			if recovery == 0 {
				recovery = EventSubscriptionCapacityCommandRecoverySustainDefault
			}
			condition.RecoveryThreshold = float64(EventSubscriptionCapacityCommandMaxReceipts - remaining)
			condition.RecoverySustainSeconds = recovery.Seconds()
		}
		out = append(out, condition)
	}
	reasons := make([]string, 0, len(NewEventPendingWaitingReasonCounts()))
	for reason := range NewEventPendingWaitingReasonCounts() {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		if n := o.WaitingReasonCounts[reason]; n > 0 {
			out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: "waiting_reason." + reason + ".recipients", State: "unknown", Threshold: float64(n)}})
		}
		if d := o.WaitingReasonDurations[reason]; d > 0 {
			out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: "waiting_reason." + reason + ".seconds", State: "unknown", Threshold: d.Seconds()}, sustained: true})
		}
	}
	if o.SLO != nil {
		for _, v := range sloTargets(*o.SLO) {
			out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: v.Metric, State: "unknown", Threshold: v.Threshold, MinimumSamples: o.SLO.MinimumSamples, SustainSeconds: o.SLO.Sustain.Seconds()}})
		}
	}
	out = append(out, newRateRiskConditions(o.RateRisk)...)
	return out
}

// WatchEventConsumerDeliveryHealth emits one local notification per confirmed
// transition. DurableAlerts exposes a bounded checkpoint outbox; callers must
// persist progress before delivering notifications. PollAcknowledgements can
// remove processed alerts between observations through the same progress hook.
func (c *Client) WatchEventConsumerDeliveryHealth(ctx context.Context, app, sub string, o EventDeliveryHealthWatchOptions) (EventDeliveryHealthWatchResult, error) {
	if o.RateRisk != nil {
		snapshot := *o.RateRisk
		snapshot.DeadlineWindow = cloneDeadlineWindowOptions(snapshot.DeadlineWindow)
		o.RateRisk = &snapshot
	}
	if o.SLO != nil {
		snapshot := *o.SLO
		o.SLO = &snapshot
	}
	out := EventDeliveryHealthWatchResult{SubscriptionID: strings.ToLower(sub), StopReason: "validation_failure", Conditions: []EventDeliveryHealthCondition{}, Alerts: []EventDeliveryHealthAlert{}}
	if err := o.validate(); err != nil {
		return out, err
	}
	if app == "" || sub == "" {
		return out, ErrValidation("app and subscription are required")
	}
	var namespace [16]byte
	if _, err := rand.Read(namespace[:]); err != nil {
		return out, fmt.Errorf("alert namespace: %w", err)
	}
	out.Session, out.StopReason = hex.EncodeToString(namespace[:]), "running"
	conditions := newDeliveryHealthConditions(o)
	if o.DurableAlerts {
		out.Checkpoint = &EventDeliveryHealthCheckpoint{Version: 1, App: app, SubscriptionID: out.SubscriptionID, Window: o.Window, Pending: []EventDeliveryHealthAlert{}}
		for _, condition := range conditions {
			if condition.RecoverySustainSeconds > 0 {
				out.Checkpoint.Version = 2
				break
			}
		}
	}
	if err := restoreDeliveryHealthCheckpoint(&out, conditions, app, o); err != nil {
		out.StopReason = "validation_failure"
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	appID := out.AppID
	rateTracker := deliveryRateTracker{}
	// Historical timestamps prevent replay; counts and sustain proof are not restored.
	for _, condition := range conditions {
		if deliveryRateRiskMetric(condition.Metric) && condition.Metric != "rate_preview.deadline_risk_recipients" && condition.observedAt.After(rateTracker.lastObserved) {
			rateTracker.lastObserved = condition.observedAt
		}
	}

	collectDeliveryHealthConditions(&out, conditions)
	for {
		out.Alerts = []EventDeliveryHealthAlert{}
		if err := pollDeliveryHealthAcknowledgements(ctx, o, &out); err != nil {
			return out, err
		}
		if o.RateRisk != nil {
			out.RateEvidence = &EventDeliveryRateEvidence{Status: "unknown", Reason: "health_read_failure", Contributors: "evidence_unavailable"}
		}
		health, err := c.GetEventConsumerDeliveryHealth(ctx, app, sub, o.Window)
		now := time.Now()
		if err != nil {
			rateTracker.previous = nil
			for _, condition := range conditions {
				updateDeliveryHealthCondition(&out, condition, 0, false, time.Time{}, now, o.MaxAge)
			}
			out.StopReason = "read_failure"
			if ctx.Err() != nil {
				out.StopReason = "cancelled"
				if ctx.Err() == context.DeadlineExceeded {
					out.StopReason = "timeout"
				}
			}
			collectDeliveryHealthConditions(&out, conditions)
			if callbackErr := deliveryHealthProgress(o, out); callbackErr != nil {
				out.StopReason = "callback_failure"
				return out, callbackErr
			}
			return out, err
		}
		out.Observations++
		out.Health = &health
		if o.SLO != nil {
			out.SLO, _ = EvaluateEventConsumerDeliverySLO(health, *o.SLO)
		}
		binding := health.Routing.AppID != "" && strings.EqualFold(health.Routing.AppID, health.Execution.AppID) && strings.EqualFold(health.Routing.SubscriptionID, sub) && strings.EqualFold(health.Execution.SubscriptionID, sub)
		if appID == "" && binding {
			appID = health.Routing.AppID
		}
		out.AppID = appID
		binding = binding && strings.EqualFold(appID, health.Routing.AppID)
		if o.RateRisk != nil {
			if binding {
				out.RateEvidence = c.readDeliveryRateEvidence(ctx, app, sub, appID, &rateTracker, o.MaxAge, o.RateRisk.DeadlineRisk > 0, o.RateRisk.DeadlineWindow)
			} else {
				rateTracker.previous = nil
				out.RateEvidence = &EventDeliveryRateEvidence{Status: "unknown", Reason: "binding_mismatch", Contributors: "evidence_unavailable"}
			}
			now = time.Now()
		}

		for _, condition := range conditions {
			if deliveryRateRiskMetric(condition.Metric) {
				updateDeliveryRateRiskCondition(&out, condition, now, o.MaxAge, binding)
				continue
			}
			if processingWindowMetric(condition.Metric) {
				updateProcessingWindowCondition(&out, condition, health, o, binding, now)
				continue
			}
			value, valid, observed := deliveryHealthValue(health, condition.Metric)
			if strings.HasPrefix(condition.Metric, "slo.") {
				for _, v := range out.SLO {
					if v.Metric == condition.Metric {
						value, valid, observed = v.Value, v.Status == "eligible", v.ObservedAt
						break
					}
				}
			}
			updateDeliveryHealthCondition(&out, condition, value, binding && valid, observed, now, o.MaxAge)
		}
		collectDeliveryHealthConditions(&out, conditions)
		if out.queueFull {
			out.StopReason = "alert_outbox_full"
		}
		if err := deliveryHealthProgress(o, out); err != nil {
			out.StopReason = "callback_failure"
			return out, err
		}
		if out.queueFull {
			if err := pollDeliveryHealthAcknowledgements(ctx, o, &out); err != nil {
				return out, err
			}
			if deliveryHealthOpeningCapacity(&out) {
				out.queueFull = false
				out.StopReason = "running"
			} else {
				return out, deliveryHealthQueueError(out)
			}
		}
		timer := time.NewTimer(o.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			out.Alerts = []EventDeliveryHealthAlert{}
			out.StopReason = "cancelled"
			if ctx.Err() == context.DeadlineExceeded {
				out.StopReason = "timeout"
			}
			return out, ctx.Err()
		case <-timer.C:
		}
	}
}

func collectDeliveryHealthConditions(out *EventDeliveryHealthWatchResult, conditions []*deliveryHealthCondition) {
	out.Conditions = make([]EventDeliveryHealthCondition, 0, len(conditions))
	out.Confirmations = nil
	for _, condition := range conditions {
		condition.ObservedAt = condition.observedAt
		out.Conditions = append(out.Conditions, condition.EventDeliveryHealthCondition)
		if condition.Metric == "capacity_command_receipt_budget_used_slots" || condition.Metric == "capacity_command_receipt_budget_full_seconds" {
			out.Confirmations = append(out.Confirmations, deliveryHealthReceiptBudgetConfirmation(condition, out.queueFull))
		} else if deliveryRateRiskMetric(condition.Metric) {
			out.Confirmations = append(out.Confirmations, deliveryHealthRateRiskConfirmation(out, condition))
		}
	}
	if out.Checkpoint != nil {
		out.Checkpoint.AppID = out.AppID
		out.Checkpoint.Session = out.Session
		out.Checkpoint.Sequence = out.Sequence
		out.Checkpoint.Conditions = append([]EventDeliveryHealthCondition{}, out.Conditions...)
	}
}

func deliveryHealthProgress(o EventDeliveryHealthWatchOptions, out EventDeliveryHealthWatchResult) error {
	if o.OnProgress == nil {
		return nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("copy delivery health progress: %w", err)
	}
	var snapshot EventDeliveryHealthWatchResult
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("copy delivery health progress: %w", err)
	}
	return o.OnProgress(snapshot)
}

func updateDeliveryHealthCondition(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, value float64, valid bool, observed, now time.Time, maxAge time.Duration) {
	failure := deliveryHealthConfirmationFailure(value, valid, observed, now, maxAge, c.observedAt)
	gap := !c.localLast.IsZero() && now.Sub(c.localLast) > maxAge
	if gap {
		loseDeliveryHealthConfirmation(out, c, now, "sample_gap")
	}
	if failure != "" {
		loseDeliveryHealthConfirmation(out, c, now, "evidence_unavailable")
		c.confirmationLossReason = failure
		return
	}
	c.observedAt, c.localLast = observed, now
	rawPositive := value > 0
	if c.sustained {
		if value > 0 {
			if c.first.IsZero() {
				c.first = now
				c.samples = 0
			}
			c.samples++
			value = now.Sub(c.first).Seconds()
		} else {
			c.first = time.Time{}
			c.samples = 0
		}
	}
	c.Value = value
	if (c.Metric == "capacity_command_receipt_budget_used_slots" || deliveryRateRiskMetric(c.Metric)) && c.RecoverySustainSeconds > 0 {
		updateDeliveryHealthReceiptBudgetRecovery(out, c, value, now)
		return
	}
	breached := value >= c.Threshold && (!c.sustained || c.samples >= 2)
	if c.SustainSeconds > 0 {
		breached = updateDeliveryHealthSLOSustain(c, value, now)
	}
	if c.resuming {
		if ((c.sustained && rawPositive) || (c.SustainSeconds > 0 && value >= c.Threshold)) && !breached {
			c.State = "unknown"
			return
		}
		c.State = "firing"
		c.resuming = false
	}
	if breached && c.State != "firing" {
		if !deliveryHealthCanOpen(out) {
			c.State = "unknown"
			return
		}
		out.reserved++
		out.Sequence++
		c.IncidentID = fmt.Sprintf("%s-%d", out.Session, out.Sequence)
		c.State = "firing"
		appendDeliveryHealthAlert(out, c, now, "opened", "threshold_breached")
	} else if !breached {
		if c.State == "firing" {
			out.Sequence++
			appendDeliveryHealthAlert(out, c, now, "resolved", "threshold_cleared")
		}
		c.State, c.IncidentID = "clear", ""
	}
}

func loseDeliveryHealthConfirmation(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, now time.Time, reason string) {
	c.confirmationLossReason = reason
	c.confirmationLossAt = now.UTC()
	if c.State == "firing" || c.resuming {
		out.Sequence++
		appendDeliveryHealthAlert(out, c, now, "confirmation_lost", reason)
	}
	c.resuming = false
	c.State, c.IncidentID, c.Value = "unknown", "", 0
	c.first, c.localLast, c.samples = time.Time{}, time.Time{}, 0
	c.recoveryFirst, c.recoverySamples = time.Time{}, 0
	c.windowEnd = time.Time{}
}

func appendDeliveryHealthAlert(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, now time.Time, transition, reason string) {
	alert := EventDeliveryHealthAlert{MetricRecordingSince: c.MetricRecordingSince, RecoveryThreshold: c.RecoveryThreshold, RecoverySustainSeconds: c.RecoverySustainSeconds, AppID: out.AppID, SubscriptionID: out.SubscriptionID, Sequence: out.Sequence, IncidentID: c.IncidentID, Metric: c.Metric, Transition: transition, Reason: reason, At: now.UTC(), Value: c.Value, Threshold: c.Threshold, MinimumSamples: c.MinimumSamples, SustainSeconds: c.SustainSeconds}
	if deliveryRateRiskMetric(c.Metric) {
		alert.RateConstraints = "evidence_unavailable"
		if out.RateEvidence != nil && out.RateEvidence.Status == "eligible" && transition != "confirmation_lost" {
			alert.RateConstraints = out.RateEvidence.Contributors
		}
	}
	out.Alerts = append(out.Alerts, alert)
	if out.Checkpoint != nil {
		out.Checkpoint.Pending = append(out.Checkpoint.Pending, alert)
	}
	if transition != "opened" {
		out.reserved--
	}
}
