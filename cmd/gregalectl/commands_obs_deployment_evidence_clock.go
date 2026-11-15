package main

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func obsDeploymentEvidenceClock(kind string, at *time.Time, maxAge float64, run bool, after, now time.Time) obsDeploymentEvidenceDetail {
	out := obsDeploymentEvidenceDetail{Kind: kind, MaxAgeSeconds: maxAge, AllowedClockSkewSeconds: api.ObsHealthClockSkewTolerance.Seconds()}
	if at == nil || at.IsZero() {
		out.Reason = "missing_timestamp"
		return out
	}
	clock := at.UTC()
	if _, err := clock.MarshalJSON(); err != nil {
		out.Reason = "invalid_metadata"
		return out
	}
	out.ObservedAt = &clock
	age := float64(now.Unix()-at.Unix()) + float64(now.Nanosecond()-at.Nanosecond())/float64(time.Second)
	if run {
		age = float64(now.UnixMilli()-at.UnixMilli()) / 1000
	}
	out.AgeSeconds = &age
	switch {
	case at.After(now.Add(api.ObsHealthClockSkewTolerance)):
		out.Reason = "future_timestamp"
	case at.Before(after):
		out.Reason = "before_deployment_boundary"
	case run && age > maxAge:
		out.Reason = "stale"
	case !run && at.Before(now.Add(-api.ObsHealthFreshnessMaxAge)):
		out.Reason = "stale"
	}
	return out
}

func obsDeploymentEvidenceSignal(kind, name string, signal api.ObsNotificationSignal, window time.Duration, after, now time.Time) *obsDeploymentEvidenceDetail {
	if signal.Reason == "incomplete_integration_evidence" {
		signal.Reason = "incomplete_evidence"
	}
	// Validate metadata relative to its own clock so an otherwise valid stale
	// signal can explain its age. Never expose a clock from malformed metadata.
	checked := signal
	if checked.ObservedAt == nil || checked.ObservedAt.IsZero() {
		checked.ObservedAt = &now
	}
	if !validReceiverSignal(checked, window, *checked.ObservedAt) {
		return &obsDeploymentEvidenceDetail{Kind: kind, Name: name, Reason: "invalid_metadata"}
	}
	out := obsDeploymentEvidenceClock(kind, signal.ObservedAt, api.ObsHealthFreshnessMaxAge.Seconds(), false, after, now)
	out.Name, out.SourceState, out.SourceReason = name, signal.State, signal.Reason
	if out.Reason == "invalid_metadata" {
		return &out
	}
	if out.Reason == "" || signal.ObservedAt == nil || signal.ObservedAt.IsZero() {
		switch signal.State {
		case "no_samples":
			out.Reason = "missing_samples"
		case "unavailable":
			out.Reason = "signal_unavailable"
		case "stale":
			if signal.ObservedAt != nil && !signal.ObservedAt.IsZero() {
				out.Reason = "stale"
			}
		}
	}
	if out.Reason == "" {
		return nil
	}
	return &out
}
