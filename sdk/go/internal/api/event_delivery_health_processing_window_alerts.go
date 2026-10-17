package api

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func processingWindowMetric(metric string) bool {
	return metric == "processing_renewal_rejection_fraction" || metric == "processing_lease_expiry_rate_per_second"
}
func processingWindowPolicy(o EventDeliveryHealthWatchOptions) (int64, time.Duration) {
	samples, sustain := o.ProcessingRenewalMinimumSamples, o.ProcessingWindowSustain
	if samples == 0 {
		samples = EventProcessingWindowAlertMinimumSamples
	}
	if sustain == 0 {
		sustain = EventProcessingWindowAlertSustain
	}
	return samples, sustain
}
func validProcessingRecordingSince(t *time.Time) bool {
	return t != nil && !t.IsZero() && t.UTC().Year() >= 1 && t.UTC().Year() <= 9999
}
func sameProcessingRecordingSince(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
func validateProcessingWindowWatch(o EventDeliveryHealthWatchOptions) error {
	for _, v := range []float64{o.ProcessingRenewalRejectionFraction, o.ProcessingLeaseExpiryRate} {
		if !finiteSLOValue(v) || v < 0 {
			return ErrValidation("invalid processing metric threshold")
		}
	}
	enabled := o.ProcessingRenewalRejectionFraction > 0 || o.ProcessingLeaseExpiryRate > 0
	if !enabled {
		if o.ProcessingMetricsRecordingSince != nil || o.ProcessingRenewalMinimumSamples != 0 || o.ProcessingWindowSustain != 0 {
			return ErrValidation("processing metric settings require an enabled rate threshold")
		}
		return nil
	}
	samples, sustain := processingWindowPolicy(o)
	if o.ProcessingRenewalRejectionFraction == 0 && o.ProcessingRenewalMinimumSamples != 0 {
		return ErrValidation("renewal sample settings require a renewal rejection threshold")
	}
	if o.ProcessingRenewalRejectionFraction > 1 || o.ProcessingLeaseExpiryRate > float64(EventDeliveryHealthWatchCountMax) || samples < 1 || samples > EventDeliveryHealthWatchCountMax || sustain < EventProcessingWindowAlertSustain || sustain > EventDeliveryHealthWatchThresholdMax || (o.ProcessingMetricsRecordingSince != nil && (!validProcessingRecordingSince(o.ProcessingMetricsRecordingSince) || o.ProcessingMetricsRecordingSince.After(time.Now()))) {
		return ErrValidation("processing rate alerts require bounded thresholds, samples, sustain of at least one minute and verified server coverage or a past recording-since override")
	}
	return nil
}
func validProcessingWindowCondition(c EventDeliveryHealthCondition) bool {
	if !processingWindowMetric(c.Metric) {
		return c.MetricRecordingSince == nil
	}
	if (c.MetricRecordingSince != nil && (!validProcessingRecordingSince(c.MetricRecordingSince) || (!c.ObservedAt.IsZero() && c.MetricRecordingSince.After(c.ObservedAt)))) || !finiteSLOValue(c.SustainSeconds) || c.SustainSeconds < EventProcessingWindowAlertSustain.Seconds() || c.SustainSeconds > EventDeliveryHealthWatchThresholdMax.Seconds() {
		return false
	}
	if c.Metric == "processing_renewal_rejection_fraction" {
		return c.MinimumSamples >= 1 && c.MinimumSamples <= EventDeliveryHealthWatchCountMax && c.Value <= 1
	}
	return c.MinimumSamples == 0 && c.Value <= float64(EventDeliveryHealthWatchCountMax)
}
func newProcessingWindowConditions(o EventDeliveryHealthWatchOptions) []*deliveryHealthCondition {
	out := []*deliveryHealthCondition{}
	samples, sustain := processingWindowPolicy(o)
	for _, v := range []struct {
		metric    string
		threshold float64
		minimum   int64
	}{
		{"processing_renewal_rejection_fraction", o.ProcessingRenewalRejectionFraction, samples},
		{"processing_lease_expiry_rate_per_second", o.ProcessingLeaseExpiryRate, 0},
	} {
		if v.threshold <= 0 {
			continue
		}
		var since *time.Time
		if o.ProcessingMetricsRecordingSince != nil {
			at := o.ProcessingMetricsRecordingSince.UTC()
			since = &at
		}
		out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: v.metric, Threshold: v.threshold, MinimumSamples: v.minimum, SustainSeconds: sustain.Seconds(), RecoveryThreshold: v.threshold, RecoverySustainSeconds: sustain.Seconds(), MetricRecordingSince: since, State: "unknown"}})
	}
	return out
}
func processingWindowClose(a, b float64) bool {
	return finiteSLOValue(a) && math.Abs(a-b) <= 1e-12*(1+math.Abs(b))
}
func processingWindowValue(h EventConsumerDeliveryHealth, c *deliveryHealthCondition, o EventDeliveryHealthWatchOptions) (float64, bool, time.Time, time.Time) {
	p := h.Processing
	if p == nil {
		return 0, false, time.Time{}, time.Time{}
	}
	if p.RenewalWindow == nil {
		return 0, false, p.ObservedAt, time.Time{}
	}
	w := p.RenewalWindow
	window, _ := EventConsumerHealthWindow(o.Window)
	start, end := EventAcknowledgmentWindowBounds(p.ObservedAt, window, w.RecordingCoverage)
	valid := strings.EqualFold(p.AppID, h.Routing.AppID) && strings.EqualFold(p.SubscriptionID, h.Routing.SubscriptionID) && p.Coverage == "retained_deferred_invocations" && w.Coverage == "recorded_full_minute_buckets" && !w.Saturated && w.BucketSeconds == int(EventAcknowledgmentMetricBucket/time.Second) && w.RetentionSeconds == int(EventAcknowledgmentMetricRetention/time.Second) && w.WindowStart.Equal(start) && w.WindowEnd.Equal(end) && start.Before(end) && processingRecordingWindowEligible(w, start, end, c.MetricRecordingSince)
	minutes := int64(end.Sub(start) / EventAcknowledgmentMetricBucket)
	if !valid || minutes < 1 || minutes > int64(EventConsumerHealthMaxWindow/EventAcknowledgmentMetricBucket) {
		return 0, false, p.ObservedAt, end
	}
	bound := minutes * EventAcknowledgmentMetricCountMax
	if w.RenewalSuccesses < 0 || w.RenewalSuccesses > bound || w.RenewalRejections < 0 || w.RenewalRejections > bound || w.LeaseExpiries < 0 || w.LeaseExpiries > bound || w.RenewalAttempts != w.RenewalSuccesses+w.RenewalRejections {
		return 0, false, p.ObservedAt, end
	}
	if c.Metric == "processing_renewal_rejection_fraction" {
		if w.RenewalAttempts < c.MinimumSamples || w.RenewalRejectionFraction == nil {
			return 0, false, p.ObservedAt, end
		}
		value := float64(w.RenewalRejections) / float64(w.RenewalAttempts)
		return value, processingWindowClose(*w.RenewalRejectionFraction, value), p.ObservedAt, end
	}
	if w.LeaseExpiryRatePerSecond == nil {
		return 0, false, p.ObservedAt, end
	}
	value := float64(w.LeaseExpiries) / end.Sub(start).Seconds()
	return value, processingWindowClose(*w.LeaseExpiryRatePerSecond, value), p.ObservedAt, end
}
func updateProcessingWindowCondition(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, h EventConsumerDeliveryHealth, o EventDeliveryHealthWatchOptions, binding bool, now time.Time) {
	value, valid, observed, end := processingWindowValue(h, c, o)
	if c.MetricRecordingSince == nil && h.Processing != nil && h.Processing.RenewalWindow != nil {
		coverage := h.Processing.RenewalWindow.RecordingCoverage
		valid = valid && coverage != nil && !coverage.ObservedAt.After(now) && now.Sub(coverage.ObservedAt) <= o.MaxAge
	}
	failure := deliveryHealthConfirmationFailure(value, binding && valid, observed, now, o.MaxAge, c.observedAt)
	if (!c.localLast.IsZero() && now.Sub(c.localLast) > o.MaxAge) || (!c.windowEnd.IsZero() && end.After(c.windowEnd.Add(EventAcknowledgmentMetricBucket))) {
		loseDeliveryHealthConfirmation(out, c, now, "sample_gap")
	}
	if failure != "" {
		loseDeliveryHealthConfirmation(out, c, now, "evidence_unavailable")
		c.confirmationLossReason = failure
		return
	}
	c.observedAt, c.localLast = observed, now
	changed := (value >= c.Threshold) != (c.Value >= c.Threshold)
	previousEnd := c.windowEnd
	c.windowEnd = end
	c.Value = value
	// A fresh database read of the same closed window is liveness, not another window sample.
	if end.Equal(previousEnd) && !changed {
		return
	}
	if value >= c.Threshold {
		c.recoveryFirst, c.recoverySamples = time.Time{}, 0
		if c.first.IsZero() {
			c.first = now
			c.samples = 0
		}
		c.samples++
		if c.samples < EventProcessingWindowAlertMinimumWindows || now.Sub(c.first).Seconds() < c.SustainSeconds {
			if c.State != "firing" {
				c.State = "unknown"
			}
			return
		}
		if c.resuming {
			c.State = "firing"
			c.resuming = false
			return
		}
		if c.State == "firing" {
			return
		}
		if !deliveryHealthCanOpen(out) {
			c.State = "unknown"
			return
		}
		out.reserved++
		out.Sequence++
		c.IncidentID = fmt.Sprintf("%s-%d", out.Session, out.Sequence)
		c.State = "firing"
		appendDeliveryHealthAlert(out, c, now, "opened", "threshold_breached")
		return
	}
	c.first, c.samples = time.Time{}, 0
	if c.recoveryFirst.IsZero() {
		c.recoveryFirst = now
		c.recoverySamples = 0
	}
	c.recoverySamples++
	if c.recoverySamples < EventProcessingWindowAlertMinimumWindows || now.Sub(c.recoveryFirst).Seconds() < c.RecoverySustainSeconds {
		return
	}
	if c.IncidentID != "" {
		out.Sequence++
		appendDeliveryHealthAlert(out, c, now, "resolved", "threshold_cleared")
	}
	c.State, c.IncidentID, c.resuming = "clear", "", false
}

func processingRecordingWindowEligible(w *EventAcknowledgmentWindowHealth, start, end time.Time, manual *time.Time) bool {
	if manual != nil {
		return !start.Before(*manual)
	}
	c := w.RecordingCoverage
	return c != nil && c.Verified && c.Reason == "verified" && !c.ObservedAt.IsZero() && c.ContinuousSince != nil && c.VerifiedThrough != nil && len(c.InventoryRevision) == 64 && strings.Trim(c.InventoryRevision, "0123456789abcdef") == "" && c.ExpectedWriters >= 2 && c.ExpectedWriters <= EventAcknowledgmentWriterRosterMax && c.ActiveWriters == c.ExpectedWriters && c.CapabilityVersion == EventAcknowledgmentWriterCapabilityVersion && c.HeartbeatLeaseSeconds == int(EventAcknowledgmentWriterLease/time.Second) && !c.ContinuousSince.IsZero() && !c.VerifiedThrough.IsZero() && c.VerifiedThrough.Add(EventAcknowledgmentWriterLease).After(c.ObservedAt) && !c.VerifiedThrough.After(c.ObservedAt) && !c.ContinuousSince.After(*c.VerifiedThrough) && !start.Before(*c.ContinuousSince) && !end.After(*c.VerifiedThrough)
}

const EventProcessingWindowAlertMinimumSamples int64 = 10
const EventProcessingWindowAlertSustain = time.Minute
const EventProcessingWindowAlertMinimumWindows = 2
const EventAcknowledgmentMetricBucket = time.Minute
const EventAcknowledgmentMetricRetention = 48 * time.Hour
const EventAcknowledgmentMetricCountMax int64 = 1<<31 - 1

const EventAcknowledgmentWriterRosterMax = 128
const EventAcknowledgmentWriterCapabilityVersion = 1
const EventAcknowledgmentWriterLease = 30 * time.Second
