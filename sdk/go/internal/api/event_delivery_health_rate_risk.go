package api

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EventDeliveryRateRiskOptions enables preview-based Go SDK watch conditions.
// Growth counts recipients added between consecutive complete observations while
// the shared token bucket is throttled. DeadlineRisk counts modeled/expired risks.
type EventDeliveryRateRiskOptions struct {
	ThrottledGrowth int64
	DeadlineRisk    int64
	DeadlineWithin  time.Duration
	Sustain         time.Duration
	RecoverySustain time.Duration
}

// EventDeliveryRateEvidence is a local proof diagnostic, never a reservation.
// Risk-detail truncation is independent of complete aggregate pending coverage.
type EventDeliveryRateEvidence struct {
	Status                    string                        `json:"status"`
	Reason                    string                        `json:"reason"`
	Preview                   *EventSubscriptionRatePreview `json:"preview,omitempty"`
	DeadlineEvidenceAvailable bool                          `json:"deadline_evidence_available"`
	DeadlineEvidenceReason    string                        `json:"deadline_evidence_reason"`
	GrowthAvailable           bool                          `json:"growth_available"`
	GrowthRecipients          int64                         `json:"growth_recipients"`
	GrowthReason              string                        `json:"growth_reason,omitempty"`
	Contributors              string                        `json:"contributors"`
}
type deliveryRateTracker struct {
	previous     *EventSubscriptionRatePreview
	lastObserved time.Time
}

func rateDeadlineWindow(metric string) (time.Duration, bool) {
	raw, ok := strings.CutPrefix(metric, "rate_preview.deadline_within_millis.")
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
	return deadline || metric == "rate_preview.throttled_growth_recipients" || metric == "rate_preview.deadline_risk_recipients"
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
	if o.DeadlineWithin < 0 || o.DeadlineWithin > EventDeliveryHealthWatchThresholdMax || (o.DeadlineWithin > 0 && (o.DeadlineWithin < EventDeliveryHealthWatchThresholdMin || o.DeadlineWithin%time.Millisecond != 0)) {
		return ErrValidation("deadline window must be whole milliseconds within watch duration bounds")
	}

	if o.ThrottledGrowth < 0 || o.DeadlineRisk < 0 || o.ThrottledGrowth > EventSubscriptionRatePreviewSampleMax || o.DeadlineRisk > EventSubscriptionRatePreviewSampleMax || o.ThrottledGrowth == 0 && o.DeadlineRisk == 0 && o.DeadlineWithin == 0 || sustain < EventDeliveryHealthWatchThresholdMin || sustain > EventDeliveryHealthWatchThresholdMax || recovery < EventDeliveryHealthWatchThresholdMin || recovery > EventDeliveryHealthWatchThresholdMax {
		return ErrValidation("rate-risk alerts require a count 1..1000, deadline window, or bounded warning/recovery sustain durations")
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
	}{{"rate_preview.throttled_growth_recipients", o.ThrottledGrowth}, {"rate_preview.deadline_risk_recipients", o.DeadlineRisk}} {
		if v.count > 0 {
			out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: v.metric, State: "unknown", Threshold: float64(v.count), MinimumSamples: 2, SustainSeconds: sustain.Seconds(), RecoveryThreshold: float64(v.count - 1), RecoverySustainSeconds: recovery.Seconds()}})
		}
	}
	if o.DeadlineWithin > 0 {
		out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: "rate_preview.deadline_within_millis." + strconv.FormatInt(o.DeadlineWithin.Milliseconds(), 10), State: "unknown", Threshold: 1, MinimumSamples: 2, SustainSeconds: sustain.Seconds(), RecoveryThreshold: 0, RecoverySustainSeconds: recovery.Seconds()}})
	}
	return out
}
func validRateRiskCondition(c EventDeliveryHealthCondition) bool {
	if _, near := rateDeadlineWindow(c.Metric); near && c.Value > 1 {
		return false
	}
	return c.MinimumSamples == 2 && finiteSLOValue(c.SustainSeconds) && c.SustainSeconds >= EventDeliveryHealthWatchThresholdMin.Seconds() && c.SustainSeconds <= EventDeliveryHealthWatchThresholdMax.Seconds() && c.Value <= EventSubscriptionRatePreviewSampleMax && math.Trunc(c.Value) == c.Value
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
func sameRatePreviewPolicy(a, b EventSubscriptionRatePreview) bool {
	return a.ExpectedRevision == b.ExpectedRevision && a.Current.Enabled == b.Current.Enabled && a.Current.IntervalMillis == b.Current.IntervalMillis && a.Current.Burst == b.Current.Burst && a.LegacyRatePerSecond == b.LegacyRatePerSecond
}

func (c *Client) readDeliveryRateEvidence(ctx context.Context, app, sub, appID string, tracker *deliveryRateTracker, maxAge time.Duration) *EventDeliveryRateEvidence {
	out := &EventDeliveryRateEvidence{Status: "unknown", Reason: "read_failure", Contributors: "evidence_unavailable"}
	var control EventSubscriptionDeliveryControl
	err := c.do(ctx, http.MethodGet, subscriptionDeliveryControlPath(app, sub), nil, &control)
	if err != nil {
		tracker.previous = nil
		return out
	}
	if !strings.EqualFold(control.AppID, appID) || !strings.EqualFold(control.SubscriptionID, sub) {
		tracker.previous = nil
		out.Reason = "binding_mismatch"
		return out
	}
	current := EventSubscriptionRateLimit{IntervalMillis: 1000, Burst: 1}
	if control.RateLimit != nil {
		current = *control.RateLimit
	}
	enabled := current.Enabled
	preview, err := c.PreviewEventSubscriptionRateLimit(ctx, app, sub, EventSubscriptionRatePreviewRequest{Enabled: &enabled, IntervalMillis: current.IntervalMillis, Burst: current.Burst})
	if err != nil {
		tracker.previous = nil
		return out
	}
	out.Preview = &preview
	reason := validateRatePreviewEvidence(preview, appID, sub)
	if reason == "" && (current.Revision != preview.ExpectedRevision || current.Enabled != preview.Current.Enabled || current.IntervalMillis != preview.Current.IntervalMillis || current.Burst != preview.Current.Burst) {
		reason = "configuration_changed"
	}
	if reason == "" {
		reason = deliveryHealthConfirmationFailure(0, true, preview.ObservedAt, time.Now(), maxAge, time.Time{})
	}
	contributors, known := previewRateContributors(preview)
	if reason == "" && !known {
		reason = "unknown_constraints"
	}
	if reason != "" {
		out.Reason = reason
		tracker.previous = nil
		return out
	}
	if !tracker.lastObserved.IsZero() && !preview.ObservedAt.After(tracker.lastObserved) {
		out.Reason = "nonadvancing_observation"
		tracker.previous = nil
		return out
	}
	tracker.lastObserved = preview.ObservedAt
	out.Status, out.Reason, out.Contributors = "eligible", "preview_evidence_available", contributors
	_, out.DeadlineEvidenceAvailable = rateDeadlineWindowValue(preview, 0)
	out.DeadlineEvidenceReason = "effective_deadlines_available"
	if !out.DeadlineEvidenceAvailable {
		out.DeadlineEvidenceReason = "deadline_evidence_unavailable"
	}

	prior := tracker.previous
	out.GrowthReason = "growth_baseline_required"
	if prior != nil {
		switch {
		case !preview.ObservedAt.After(prior.ObservedAt):
			out.Status = "unknown"
			out.Reason = "nonadvancing_observation"
			out.Contributors = "evidence_unavailable"
			tracker.previous = nil
			return out
		case preview.ObservedAt.Sub(prior.ObservedAt) > maxAge:
			out.GrowthReason = "sample_gap"
		case !sameRatePreviewPolicy(*prior, preview):
			out.GrowthReason = "configuration_changed"
		default:
			out.GrowthAvailable = true
			out.GrowthReason = "consecutive_complete_previews"
			if preview.Current.RateLimited && prior.Current.RateLimited {
				out.GrowthRecipients = max(int64(0), int64(preview.SampledPending-prior.SampledPending))
			}
		}
	}
	tracker.previous = &preview
	return out
}
func updateDeliveryRateRiskCondition(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, now time.Time, maxAge time.Duration, binding bool) {
	evidence := out.RateEvidence
	valid := binding && evidence != nil && evidence.Status == "eligible" && evidence.Preview != nil
	value := float64(0)
	var observed time.Time
	if evidence != nil && evidence.Preview != nil {
		observed = evidence.Preview.ObservedAt
	}
	if valid {
		if within, isDeadline := rateDeadlineWindow(c.Metric); isDeadline {
			value, valid = rateDeadlineWindowValue(*evidence.Preview, within)
		} else if c.Metric == "rate_preview.deadline_risk_recipients" {
			value = float64(evidence.Preview.DeadlineRiskCount)
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

func rateDeadlineWindowValue(p EventSubscriptionRatePreview, within time.Duration) (float64, bool) {
	if p.DeadlineCoverage != "sampled_effective_pending_deadlines" || p.DeadlineRecipients == nil || *p.DeadlineRecipients < p.DeadlineRiskCount || *p.DeadlineRecipients > p.SampledPending || (*p.DeadlineRecipients == 0) != (p.EarliestDeadlineAt == nil) {
		return 0, false
	}
	if p.EarliestDeadlineAt == nil {
		return 0, true
	}
	for _, risk := range p.DeadlineRisks {
		if p.EarliestDeadlineAt.After(risk.DeadlineAt) {
			return 0, false
		}
	}
	if p.AlreadyExpiredCount > 0 && p.EarliestDeadlineAt.After(p.ObservedAt) {
		return 0, false
	}
	if p.EarliestDeadlineAt.IsZero() {
		return 0, false
	}
	if !p.EarliestDeadlineAt.After(p.ObservedAt.Add(within)) {
		return 1, true
	}
	return 0, true
}

const (
	EventDeliveryRateRiskSustainDefault   = time.Minute
	EventDeliveryRateRiskRecoveryDefault  = time.Minute
	EventDeliveryRateContributorMaxBytes  = 512
	EventSubscriptionRatePreviewSampleMax = 1000
	EventSubscriptionRatePreviewRiskMax   = 100
	EventSubscriptionDrainRateMax         = 100
)
