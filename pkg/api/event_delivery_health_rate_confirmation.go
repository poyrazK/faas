package api

// deliveryHealthRateRiskConfirmation projects local proof without changing its
// timers, incident, counters or checkpoint. Historical samples never become proof.
func deliveryHealthRateRiskConfirmation(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition) EventDeliveryHealthConfirmation {
	d := deliveryHealthReceiptBudgetConfirmation(c, out.queueFull)
	d.RequiredSamples = 2
	if c.confirmationLossAt.IsZero() {
		d.LastConfirmationLossReason = ""
	}
	if d.Phase == "warning_pending" || d.Phase == "recovery_pending" || d.Phase == "notification_blocked" {
		return d
	}
	if c.State == "clear" {
		d.Reason = "threshold_not_breached"
		return d
	}
	if c.State == "firing" {
		return d
	}
	// Timing/order/value failures from the condition check take precedence over
	// an otherwise eligible read or a missing growth comparison.
	switch c.confirmationLossReason {
	case "future_observation", "stale_observation", "repeated_observation", "out_of_order_observation", "missing_observation", "invalid_value", "sample_gap":
		d.Reason = c.confirmationLossReason
		return d
	}
	reason := deliveryHealthRateEvidenceReason(out.RateEvidence, c.Metric)
	if reason == "" {
		return d
	}
	d.Reason = reason
	if c.Metric == "pending_summary.throttled_growth_recipients" && !c.resuming {
		switch reason {
		case "growth_baseline_required", "configuration_changed", "sample_gap":
			d.Phase = "baseline_pending"
		}
	}
	return d
}
func deliveryHealthRateEvidenceReason(e *EventDeliveryRateEvidence, metric string) string {
	if e == nil {
		return ""
	}
	if e.Status != "eligible" {
		return e.Reason
	}
	_, minimum, ratio := deadlineRatioWindow(metric)
	_, _, _, window := deadlineCountWindow(metric)
	if window || ratio {
		if !e.WindowEvidenceAvailable {
			return e.WindowEvidenceReason
		}
		if ratio && e.DeadlineWindow != nil && e.DeadlineWindow.Summary.DeadlineRecipients < minimum {
			return "insufficient_deadline_recipients"
		}
	} else if metric == "rate_preview.deadline_risk_recipients" {
		if !e.PreviewEvidenceAvailable {
			return e.PreviewEvidenceReason
		}
	} else if metric == "pending_summary.throttled_growth_recipients" && !e.GrowthAvailable {
		return e.GrowthReason
	}
	return ""
}
