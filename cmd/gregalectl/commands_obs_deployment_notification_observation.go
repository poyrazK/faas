package main

import (
	"cmp"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

// Notification history uses only the already fetched and projected preview.
// Absence from a preview or loss of source evidence never establishes recovery.
func (r *obsDeploymentNotificationRecorder) observe(serving *api.ObsServingHealth, at time.Time, attempt int) {
	now := at.UTC()
	r.history.LastObservedAt = &now
	section, reason := obsDeploymentNotificationSection(serving, r.after, now)
	r.history.SourceState, r.history.SourceReason = "unknown", reason
	seen := map[obsDeploymentNotificationKey]bool{}
	incomplete := false
	if section != nil {
		r.history.SourceState, r.history.SourceReason = section.State, section.Reason
		r.history.MaxPreviewOmitted = max(r.history.MaxPreviewOmitted, section.Omitted)
		for _, item := range section.Channels {
			if item.EvaluatedAt != nil && !obsDeploymentFresh(item.EvaluatedAt, r.after, now) {
				incomplete = true
				item = api.ObsServingProbeNotification{Node: item.Node, Channel: item.Channel, State: "unobserved", Reason: "awaiting_post_deployment_evidence"}
			}
			seen[obsDeploymentNotificationKey{item.Node, item.Channel}] = true
			r.record(item, at, attempt)
		}
		if section.Truncated {
			reason = "not_in_preview"
		} else {
			reason = "not_in_current_expectations"
		}
	}
	if section == nil || incomplete || section.State == "unknown" || section.State == "partial" || section.Truncated {
		r.history.IncompleteObservations++
	}
	keys := make([]obsDeploymentNotificationKey, 0, len(r.channels))
	for key := range r.channels {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b obsDeploymentNotificationKey) int {
		if order := cmp.Compare(a.node, b.node); order != 0 {
			return order
		}
		return cmp.Compare(a.channel, b.channel)
	})
	for _, key := range keys {
		r.record(api.ObsServingProbeNotification{Node: key.node, Channel: key.channel, State: "unobserved", Reason: reason}, at, attempt)
	}
}

func obsDeploymentNotificationSection(serving *api.ObsServingHealth, after, now time.Time) (*api.ObsServingProbeNotificationHealth, string) {
	if serving == nil {
		return nil, "serving_snapshot_unavailable"
	}
	if serving.ProbeNotifications == nil {
		return nil, "server_does_not_report_probe_notifications"
	}
	if !operatorobs.ServingProbeNotificationsValid(*serving) {
		return nil, "metadata_inconsistent"
	}
	section := serving.ProbeNotifications
	if section.ExpectedChannels == nil {
		return nil, section.Reason
	}
	if !obsDeploymentFresh(&serving.GeneratedAt, after, now) || !obsDeploymentFresh(serving.Inventory.ObservedAt, after, now) {
		return nil, "awaiting_post_deployment_evidence"
	}
	// Validate clocks at observation time too. Do not re-stamp source data in
	// the retained event when a response crossed a freshness boundary in transit.
	current := *serving
	current.GeneratedAt = now
	if !operatorobs.ServingProbeNotificationsValid(current) {
		return nil, "notification_snapshot_not_current"
	}
	return section, ""
}

func projectObsDeploymentNotificationEvent(item api.ObsServingProbeNotification, at time.Time, attempt int, after time.Time) obsDeploymentNotificationEvent {
	out := obsDeploymentNotificationEvent{At: at.UTC(), Attempt: attempt, Node: item.Node, Channel: item.Channel,
		State: item.State, Reason: item.Reason, EvidenceReason: item.EvidenceReason, FailureCode: item.FailureCode,
		EvaluatedAt: obsDeploymentEvidencePtr(item.EvaluatedAt), LastRunAt: obsDeploymentEvidencePtr(item.LastRunAt),
		LastAttemptAt: obsDeploymentEvidencePtr(item.LastAttemptAt), LastSuccessAt: obsDeploymentEvidencePtr(item.LastSuccessAt)}
	if out.LastAttemptAt != nil {
		postBoundary := !out.LastAttemptAt.Before(after)
		out.AttemptAtOrAfterBoundary = &postBoundary
	}
	return out
}
