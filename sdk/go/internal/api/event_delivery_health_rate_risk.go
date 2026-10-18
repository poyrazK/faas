package api

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EventDeliveryRateRiskOptions enables summary/model-based Go SDK watch conditions.
// Growth measures net pending-count increases between complete observations while
// the shared token bucket is throttled. DeadlineRisk counts modeled/expired risks.
type EventDeliveryRateRiskOptions struct {
	DeadlineWindow  *EventDeliveryDeadlineWindowOptions
	ThrottledGrowth int64
	DeadlineRisk    int64
	DeadlineWithin  time.Duration
	Sustain         time.Duration
	RecoverySustain time.Duration
}

// EventDeliveryRateEvidence is a local proof diagnostic, never a reservation.
// Risk-detail truncation is independent of complete aggregate pending coverage.
type EventDeliveryRateEvidence struct {
	DeadlineWindow            *EventSubscriptionDeadlineWindow `json:"deadline_window,omitempty"`
	WindowEvidenceAvailable   bool                             `json:"window_evidence_available"`
	WindowEvidenceReason      string                           `json:"window_evidence_reason"`
	Status                    string                           `json:"status"`
	Reason                    string                           `json:"reason"`
	Summary                   *EventSubscriptionPendingSummary `json:"summary,omitempty"`
	PreviewEvidenceAvailable  bool                             `json:"preview_evidence_available"`
	PreviewEvidenceReason     string                           `json:"preview_evidence_reason"`
	Preview                   *EventSubscriptionRatePreview    `json:"preview,omitempty"`
	DeadlineEvidenceAvailable bool                             `json:"deadline_evidence_available"`
	DeadlineEvidenceReason    string                           `json:"deadline_evidence_reason"`
	GrowthAvailable           bool                             `json:"growth_available"`
	GrowthRecipients          int64                            `json:"growth_recipients"`
	GrowthReason              string                           `json:"growth_reason,omitempty"`
	Contributors              string                           `json:"contributors"`
}
type deliveryRateTracker struct {
	previous     *EventSubscriptionPendingSummary
	lastObserved time.Time
}

func rateDeadlineWindow(metric string) (time.Duration, bool) {
	raw, ok := strings.CutPrefix(metric, "pending_summary.deadline_within_millis.")
	if !ok {
		return 0, false
	}
	millis, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || millis < EventDeliveryHealthWatchThresholdMin.Milliseconds() || millis > EventDeliveryHealthWatchThresholdMax.Milliseconds() || strconv.FormatInt(millis, 10) != raw {
		return 0, false
	}
	return time.Duration(millis) * time.Millisecond, true
}
func deliveryRateRiskMetric(metric string) bool {
	_, deadline := rateDeadlineWindow(metric)
	_, _, _, countWindow := deadlineCountWindow(metric)
	_, _, ratioWindow := deadlineRatioWindow(metric)
	return countWindow || ratioWindow || deadline || metric == "pending_summary.throttled_growth_recipients" || metric == "rate_preview.deadline_risk_recipients"
}
func rateRiskPolicy(o EventDeliveryRateRiskOptions) (time.Duration, time.Duration) {
	sustain, recovery := o.Sustain, o.RecoverySustain
	if sustain == 0 {
		sustain = EventDeliveryRateRiskSustainDefault
	}
	if recovery == 0 {
		recovery = EventDeliveryRateRiskRecoveryDefault
	}
	return sustain, recovery
}
func validateRateRiskOptions(o *EventDeliveryRateRiskOptions) error {
	if o == nil {
		return nil
	}
	sustain, recovery := rateRiskPolicy(*o)
	if err := validateDeadlineWindowOptions(o.DeadlineWindow); err != nil {
		return err
	}
	if o.DeadlineWithin < 0 || o.DeadlineWithin > EventDeliveryHealthWatchThresholdMax || (o.DeadlineWithin > 0 && (o.DeadlineWithin < EventDeliveryHealthWatchThresholdMin || o.DeadlineWithin%time.Millisecond != 0)) {
		return ErrValidation("deadline window must be whole milliseconds within watch duration bounds")
	}

	if o.ThrottledGrowth < 0 || o.DeadlineRisk < 0 || o.ThrottledGrowth > EventSubscriptionPendingSummaryCountMax || o.DeadlineRisk > EventSubscriptionRatePreviewSampleMax || o.ThrottledGrowth == 0 && o.DeadlineRisk == 0 && o.DeadlineWithin == 0 && o.DeadlineWindow == nil || sustain < EventDeliveryHealthWatchThresholdMin || sustain > EventDeliveryHealthWatchThresholdMax || recovery < EventDeliveryHealthWatchThresholdMin || recovery > EventDeliveryHealthWatchThresholdMax {
		return ErrValidation("rate-risk alerts require enabled bounded thresholds and bounded warning/recovery sustain durations")
	}
	return nil
}
func newRateRiskConditions(o *EventDeliveryRateRiskOptions) []*deliveryHealthCondition {
	if o == nil {
		return nil
	}
	sustain, recovery := rateRiskPolicy(*o)
	out := []*deliveryHealthCondition{}
	for _, v := range []struct {
		metric string
		count  int64
	}{{"pending_summary.throttled_growth_recipients", o.ThrottledGrowth}, {"rate_preview.deadline_risk_recipients", o.DeadlineRisk}} {
		if v.count > 0 {
			out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: v.metric, State: "unknown", Threshold: float64(v.count), MinimumSamples: 2, SustainSeconds: sustain.Seconds(), RecoveryThreshold: float64(v.count - 1), RecoverySustainSeconds: recovery.Seconds()}})
		}
	}
	if o.DeadlineWithin > 0 {
		out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: "pending_summary.deadline_within_millis." + strconv.FormatInt(o.DeadlineWithin.Milliseconds(), 10), State: "unknown", Threshold: 1, MinimumSamples: 2, SustainSeconds: sustain.Seconds(), RecoveryThreshold: 0, RecoverySustainSeconds: recovery.Seconds()}})
	}
	out = append(out, newDeadlineWindowConditions(o.DeadlineWindow, sustain, recovery)...)
	return out
}
func validRateRiskCondition(c EventDeliveryHealthCondition) bool {
	if _, near := rateDeadlineWindow(c.Metric); near && c.Value > 1 {
		return false
	}
	return c.MinimumSamples == 2 && finiteSLOValue(c.SustainSeconds) && c.SustainSeconds >= EventDeliveryHealthWatchThresholdMin.Seconds() && c.SustainSeconds <= EventDeliveryHealthWatchThresholdMax.Seconds() && validRateRiskValue(c.Metric, c.Value)
}
func knownRateContributor(s string) bool {
	switch s {
	case "rate_limited", "subscription_paused", "legacy_rate_limit", "capacity_consumer", "capacity_app", "capacity_account", "active_leases", "scheduled_retries", "circuit_open", "circuit_half_open", "circuit_draining", "clock_uncertain", "sample_truncated", "estimate_overflow", "no_observed_constraints", "evidence_unavailable":
		return true
	}
	return false
}
func validRateContributors(metric, summary string) bool {
	if !deliveryRateRiskMetric(metric) {
		return summary == ""
	}
	if summary == "" || len(summary) > EventDeliveryRateContributorMaxBytes {
		return false
	}
	parts := strings.Split(summary, ",")
	previous := ""
	for _, part := range parts {
		if !knownRateContributor(part) || part <= previous || len(parts) > 1 && (part == "no_observed_constraints" || part == "evidence_unavailable") {
			return false
		}
		previous = part
	}
	return true
}
func previewRateContributors(p EventSubscriptionRatePreview) (string, bool) {
	seen := map[string]bool{}
	for _, constraint := range p.Constraints {
		if !knownRateContributor(constraint) || seen[constraint] || constraint == "no_observed_constraints" || constraint == "evidence_unavailable" {
			return "", false
		}
		seen[constraint] = true
	}
	if p.Current.RateLimited {
		seen["rate_limited"] = true
	}
	if p.Paused {
		seen["subscription_paused"] = true
	}
	if p.LegacyRatePerSecond > 0 {
		seen["legacy_rate_limit"] = true
	}
	if p.CircuitState != "closed" {
		seen["circuit_"+p.CircuitState] = true
	}
	scopes := map[string]bool{}
	for _, scope := range p.CapacityBlockingScopes {
		if (scope != "consumer" && scope != "app" && scope != "account") || scopes[scope] {
			return "", false
		}
		scopes[scope] = true
		seen["capacity_"+scope] = true
	}

	if len(seen) == 0 {
		seen["no_observed_constraints"] = true
	}
	parts := make([]string, 0, len(seen))
	for part := range seen {
		parts = append(parts, part)
	}
	sort.Strings(parts)
	summary := strings.Join(parts, ",")
	return summary, len(summary) <= EventDeliveryRateContributorMaxBytes
}
func validateRatePreviewEvidence(p EventSubscriptionRatePreview, appID, sub string) string {
	if !strings.EqualFold(p.AppID, appID) || !strings.EqualFold(p.SubscriptionID, sub) {
		return "binding_mismatch"
	}
	if !p.ReadOnly || !p.Advisory || p.Coverage != "retained_captured_pending_application_recipients" || p.SampleLimit != EventSubscriptionRatePreviewSampleMax || p.SampledPending < 0 || p.SampledPending > p.SampleLimit || p.DeadlineRiskCount < 0 || p.DeadlineRiskCount > p.SampledPending || p.AlreadyExpiredCount < 0 || p.AlreadyExpiredCount > p.DeadlineRiskCount {
		return "invalid_preview"
	}
	if !p.Complete || p.Truncated {
		return "incomplete_preview"
	}
	if len(p.DeadlineRisks) != min(p.DeadlineRiskCount, EventSubscriptionRatePreviewRiskMax) || p.RisksTruncated != (p.DeadlineRiskCount > EventSubscriptionRatePreviewRiskMax) {
		return "invalid_risk_coverage"
	}
	if p.RateOnlyDrainMillis == nil || *p.RateOnlyDrainMillis < 0 || p.CapacityBlockingScopes == nil || p.Constraints == nil || p.DeadlineRisks == nil {
		return "missing_preview_evidence"
	}
	for _, constraint := range p.Constraints {
		if constraint == "clock_uncertain" || constraint == "sample_truncated" || constraint == "estimate_overflow" {
			return "incomplete_preview"
		}
	}
	if !validRatePreviewRiskDetails(p) {
		return "invalid_risk_details"
	}
	if p.ExpectedRevision < 0 || p.Current.Revision != p.ExpectedRevision || p.Projected.Revision != p.ExpectedRevision || !p.Current.ObservedAt.Equal(p.ObservedAt) || !p.Projected.ObservedAt.Equal(p.ObservedAt) {
		return "invalid_revision"
	}
	for _, s := range []EventSubscriptionRateLimit{p.Current, p.Projected} {
		if s.IntervalMillis < EventSubscriptionRateIntervalMinMillis || s.IntervalMillis > EventSubscriptionRateIntervalMaxMillis || s.Burst < 1 || s.Burst > EventSubscriptionRateBurstMax || s.AvailableTokens < 0 || s.AvailableTokens > s.Burst || s.RateLimited != (s.Enabled && s.AvailableTokens == 0) || !s.Enabled && s.AvailableTokens != 0 || s.RateLimited != (s.NextAvailableAt != nil) {
			return "invalid_capacity"
		}
		if s.NextAvailableAt != nil && (!s.NextAvailableAt.After(p.ObservedAt) || s.NextAvailableAt.Sub(p.ObservedAt) > time.Duration(s.IntervalMillis)*time.Millisecond) {
			return "invalid_capacity"
		}
	}
	if p.Current.Enabled != p.Projected.Enabled || p.Current.IntervalMillis != p.Projected.IntervalMillis || p.Current.Burst != p.Projected.Burst || p.Current.AvailableTokens != p.Projected.AvailableTokens {
		return "configuration_changed"
	}
	if p.LegacyRatePerSecond < 0 || p.LegacyRatePerSecond > EventSubscriptionDrainRateMax {
		return "invalid_legacy_rate"
	}
	switch p.CircuitState {
	case "closed", "open", "half_open", "draining":
	default:
		return "unknown_circuit_state"
	}
	return ""
}
func rateRiskMetricValueMax(metric string) float64 {
	if _, _, ratio := deadlineRatioWindow(metric); ratio {
		return float64(EventDeliveryDeadlineRatioBasisPointsMax)
	}
	if metric == "rate_preview.deadline_risk_recipients" {
		return EventSubscriptionRatePreviewSampleMax
	}
	return float64(EventSubscriptionPendingSummaryCountMax)
}
func sameRateSummaryPolicy(a, b EventSubscriptionPendingSummary) bool {
	return a.Current.Revision == b.Current.Revision && a.Current.Enabled == b.Current.Enabled && a.Current.IntervalMillis == b.Current.IntervalMillis && a.Current.Burst == b.Current.Burst && a.LegacyRatePerSecond == b.LegacyRatePerSecond
}
func summaryRateContributors(p EventSubscriptionPendingSummary) (string, bool) {
	return previewRateContributors(EventSubscriptionRatePreview{Current: p.Current, Paused: p.Paused, LegacyRatePerSecond: p.LegacyRatePerSecond, CircuitState: p.CircuitState, CapacityBlockingScopes: p.CapacityBlockingScopes, Constraints: p.Constraints})
}
func validatePendingSummaryEvidence(p EventSubscriptionPendingSummary, appID, sub string) string {
	if !strings.EqualFold(p.AppID, appID) || !strings.EqualFold(p.SubscriptionID, sub) {
		return "binding_mismatch"
	}
	if !p.ReadOnly || !p.Complete || p.Coverage != "all_retained_captured_pending_application_recipients" {
		return "incomplete_pending_summary"
	}
	if p.PendingRecipients < 0 || p.PendingRecipients > EventSubscriptionPendingSummaryCountMax || p.DeadlineRecipients < 0 || p.DeadlineRecipients > p.PendingRecipients || p.AlreadyExpiredCount < 0 || p.AlreadyExpiredCount > p.DeadlineRecipients || (p.DeadlineRecipients == 0) != (p.EarliestDeadlineAt == nil) {
		return "invalid_summary_counts"
	}
	if p.EarliestDeadlineAt != nil && (p.EarliestDeadlineAt.IsZero() || (!p.EarliestDeadlineAt.After(p.ObservedAt)) != (p.AlreadyExpiredCount > 0)) {
		return "invalid_summary_deadlines"
	}
	// Reuse the rate-preview's control/capacity validation with an empty model;
	// complete aggregate counters are validated independently above.
	zero := int64(0)
	controls := EventSubscriptionRatePreview{Coverage: "retained_captured_pending_application_recipients", AppID: p.AppID, SubscriptionID: p.SubscriptionID, ObservedAt: p.ObservedAt, ReadOnly: true, Advisory: true, Complete: true, SampleLimit: EventSubscriptionRatePreviewSampleMax, ExpectedRevision: p.Current.Revision, Current: p.Current, Projected: p.Current, Paused: p.Paused, LegacyRatePerSecond: p.LegacyRatePerSecond, CircuitState: p.CircuitState, CapacityBlockingScopes: p.CapacityBlockingScopes, Constraints: p.Constraints, RateOnlyDrainMillis: &zero, DeadlineRisks: []EventSubscriptionRateDeadlineRisk{}}
	return validateRatePreviewEvidence(controls, appID, sub)
}

func (c *Client) readDeliveryRateEvidence(ctx context.Context, app, sub, appID string, tracker *deliveryRateTracker, maxAge time.Duration, modeled bool, window *EventDeliveryDeadlineWindowOptions) *EventDeliveryRateEvidence {
	out := &EventDeliveryRateEvidence{Status: "unknown", Reason: "summary_read_failure", Contributors: "evidence_unavailable", DeadlineEvidenceReason: "deadline_evidence_unavailable", PreviewEvidenceReason: "not_requested"}
	var summary EventSubscriptionPendingSummary
	var err error
	if window == nil {
		summary, err = c.GetEventSubscriptionPendingSummary(ctx, app, sub)
	} else {
		var counts EventSubscriptionDeadlineWindow
		counts, err = c.GetEventSubscriptionDeadlineWindow(ctx, app, sub, window.Within.Milliseconds())
		out.DeadlineWindow = &counts
		summary = counts.Summary
		out.WindowEvidenceAvailable = err == nil && counts.WithinMillis == window.Within.Milliseconds() && EventSubscriptionDeadlineWindowCountsComplete(counts)
		out.WindowEvidenceReason = "deadline_window_evidence_unavailable"
		if out.WindowEvidenceAvailable {
			out.WindowEvidenceReason = "complete_deadline_window_available"
		}
	}
	if err != nil {
		tracker.previous = nil
		return out
	}
	out.Summary = &summary
	reason := validatePendingSummaryEvidence(summary, appID, sub)
	if reason == "" {
		reason = deliveryHealthConfirmationFailure(0, true, summary.ObservedAt, time.Now(), maxAge, time.Time{})
	}
	contributors, known := summaryRateContributors(summary)
	if reason == "" && !known {
		reason = "unknown_constraints"
	}
	if reason == "" && !tracker.lastObserved.IsZero() && !summary.ObservedAt.After(tracker.lastObserved) {
		reason = "nonadvancing_observation"
	}
	if reason != "" {
		out.Reason = reason
		tracker.previous = nil
		return out
	}
	tracker.lastObserved = summary.ObservedAt
	out.Status, out.Reason, out.Contributors = "eligible", "complete_pending_summary_available", contributors
	out.DeadlineEvidenceAvailable = true
	out.DeadlineEvidenceReason = "complete_effective_deadlines_available"
	prior := tracker.previous
	out.GrowthReason = "growth_baseline_required"
	if prior != nil {
		switch {
		case summary.ObservedAt.Sub(prior.ObservedAt) > maxAge:
			out.GrowthReason = "sample_gap"
		case !sameRateSummaryPolicy(*prior, summary):
			out.GrowthReason = "configuration_changed"
		default:
			out.GrowthAvailable = true
			out.GrowthReason = "consecutive_complete_summaries"
			if summary.Current.RateLimited && prior.Current.RateLimited {
				out.GrowthRecipients = max(int64(0), summary.PendingRecipients-prior.PendingRecipients)
			}
		}
	}
	tracker.previous = &summary
	if modeled {
		c.readDeliveryRateModel(ctx, app, sub, appID, summary, maxAge, out)
	}
	return out
}
func (c *Client) readDeliveryRateModel(ctx context.Context, app, sub, appID string, summary EventSubscriptionPendingSummary, maxAge time.Duration, out *EventDeliveryRateEvidence) {
	out.PreviewEvidenceReason = "preview_read_failure"
	enabled := summary.Current.Enabled
	preview, err := c.PreviewEventSubscriptionRateLimit(ctx, app, sub, EventSubscriptionRatePreviewRequest{Enabled: &enabled, IntervalMillis: summary.Current.IntervalMillis, Burst: summary.Current.Burst})
	if err != nil {
		return
	}
	out.Preview = &preview
	reason := validateRatePreviewEvidence(preview, appID, sub)
	if reason == "" && (summary.Current.Revision != preview.ExpectedRevision || summary.Current.Enabled != preview.Current.Enabled || summary.Current.IntervalMillis != preview.Current.IntervalMillis || summary.Current.Burst != preview.Current.Burst) {
		reason = "configuration_changed"
	}
	if reason == "" {
		reason = deliveryHealthConfirmationFailure(0, true, preview.ObservedAt, time.Now(), maxAge, time.Time{})
	}
	_, known := previewRateContributors(preview)
	if reason == "" && !known {
		reason = "unknown_constraints"
	}
	if reason != "" {
		out.PreviewEvidenceReason = reason
		return
	}
	out.PreviewEvidenceAvailable = true
	out.PreviewEvidenceReason = "complete_sample_model_available"
}
func updateDeliveryRateRiskCondition(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, now time.Time, maxAge time.Duration, binding bool) {
	evidence := out.RateEvidence
	valid := binding && evidence != nil && evidence.Status == "eligible" && evidence.Summary != nil
	value := float64(0)
	var observed time.Time
	if evidence != nil && evidence.Summary != nil {
		observed = evidence.Summary.ObservedAt
	}
	if valid {
		if within, minimum, isRatio := deadlineRatioWindow(c.Metric); isRatio {
			valid = evidence.WindowEvidenceAvailable && evidence.DeadlineWindow != nil && evidence.DeadlineWindow.WithinMillis == within.Milliseconds()
			if valid {
				value, valid = deadlineRatioValue(*evidence.DeadlineWindow, minimum)
			}
		} else if within, expired, reason, isWindow := deadlineCountWindow(c.Metric); isWindow {
			valid = evidence.WindowEvidenceAvailable && evidence.DeadlineWindow != nil && evidence.DeadlineWindow.WithinMillis == within.Milliseconds()
			if valid {
				value, valid = deadlineWindowCountValue(*evidence.DeadlineWindow, expired, reason)
			}
		} else if within, isDeadline := rateDeadlineWindow(c.Metric); isDeadline {
			if evidence.Summary.EarliestDeadlineAt != nil && !evidence.Summary.EarliestDeadlineAt.After(observed.Add(within)) {
				value = 1
			}
		} else if c.Metric == "rate_preview.deadline_risk_recipients" {
			valid = evidence.PreviewEvidenceAvailable && evidence.Preview != nil
			if valid {
				value = float64(evidence.Preview.DeadlineRiskCount)
				observed = evidence.Preview.ObservedAt
			}
		} else {
			valid = evidence.GrowthAvailable
			value = float64(evidence.GrowthRecipients)
			if !valid && evidence.GrowthReason == "growth_baseline_required" && c.resuming {
				c.State = "unknown"
				c.confirmationLossReason = "growth_baseline_required"
				return
			}
		}
	}
	updateDeliveryHealthCondition(out, c, value, valid, observed, now, maxAge)
}

func validRatePreviewRiskDetails(p EventSubscriptionRatePreview) bool {
	seen := map[[2]string]bool{}
	expired := 0
	for _, risk := range p.DeadlineRisks {
		key := [2]string{risk.EventSource, risk.EventID}
		if risk.EventSource == "" || risk.EventID == "" || seen[key] || risk.AcceptedAt.IsZero() || risk.AcceptedAt.After(p.ObservedAt) || !risk.DeadlineAt.After(risk.AcceptedAt) {
			return false
		}
		seen[key] = true
		switch risk.Reason {
		case "already_expired":
			if risk.DeadlineAt.After(p.ObservedAt) || risk.ModeledAdmissionAt != nil {
				return false
			}
			expired++
		case "modeled_deadline_risk":
			if !risk.DeadlineAt.After(p.ObservedAt) || risk.ModeledAdmissionAt == nil || risk.ModeledAdmissionAt.Before(risk.DeadlineAt) {
				return false
			}
		default:
			return false
		}
	}
	return expired <= p.AlreadyExpiredCount && (p.RisksTruncated || expired == p.AlreadyExpiredCount)
}

const (
	EventDeliveryRateRiskSustainDefault   = time.Minute
	EventDeliveryRateRiskRecoveryDefault  = time.Minute
	EventDeliveryRateContributorMaxBytes  = 512
	EventSubscriptionRatePreviewSampleMax = 1000
	EventSubscriptionRatePreviewRiskMax   = 100
	EventSubscriptionDrainRateMax         = 100
)

const EventSubscriptionPendingSummaryCountMax int64 = 9007199254740991
