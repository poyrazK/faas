package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentNotificationEvent struct {
	At                        time.Time  `json:"at"`
	Attempt                   int        `json:"attempt"`
	Node                      string     `json:"node"`
	Channel                   string     `json:"channel"`
	State                     string     `json:"state"`
	Reason                    string     `json:"reason,omitempty"`
	EvidenceReason            string     `json:"evidence_reason,omitempty"`
	FailureCode               string     `json:"failure_code,omitempty"`
	EvaluatedAt               *time.Time `json:"evaluated_at"`
	LastRunAt                 *time.Time `json:"last_run_at"`
	LastAttemptAt             *time.Time `json:"last_attempt_at"`
	LastSuccessAt             *time.Time `json:"last_success_at"`
	AttemptAtOrAfterBoundary  *bool      `json:"attempt_at_or_after_boundary"`
	LatestObservedFailureAt   *time.Time `json:"latest_observed_failure_at,omitempty"`
	UnresolvedObservedFailure bool       `json:"unresolved_observed_failure"`
	LaterSuccessAfterFailure  bool       `json:"later_success_after_failure"`
}

type obsDeploymentNotificationHistory struct {
	Scope                      string                           `json:"scope"`
	ItemLimit                  int                              `json:"item_limit"`
	ByteLimit                  int                              `json:"byte_limit"`
	TrackedChannelLimit        int                              `json:"tracked_channel_limit"`
	PreviewLimit               int                              `json:"preview_limit"`
	RetainedBytes              int                              `json:"retained_bytes"`
	DroppedEvents              int                              `json:"dropped_events"`
	EnvelopeOmittedEvents      int                              `json:"envelope_omitted_events"`
	DroppedChannelObservations int                              `json:"dropped_channel_observations"`
	TrackedChannels            int                              `json:"tracked_channels"`
	UnresolvedTrackedFailures  int                              `json:"unresolved_tracked_failures"`
	HadSubmissionFailures      bool                             `json:"had_submission_failures"`
	MaxPreviewOmitted          int                              `json:"max_preview_omitted"`
	IncompleteObservations     int                              `json:"incomplete_observations"`
	SourceState                string                           `json:"source_state"`
	SourceReason               string                           `json:"source_reason,omitempty"`
	LastObservedAt             *time.Time                       `json:"last_observed_at"`
	Truncated                  bool                             `json:"truncated"`
	Events                     []obsDeploymentNotificationEvent `json:"events"`
}

type obsDeploymentNotificationKey struct{ node, channel string }
type obsDeploymentNotificationChannel struct {
	last       obsDeploymentNotificationEvent
	failureAt  *time.Time
	unresolved bool
}
type obsDeploymentNotificationRecorder struct {
	after    time.Time
	channels map[obsDeploymentNotificationKey]obsDeploymentNotificationChannel
	history  obsDeploymentNotificationHistory
}

func newObsDeploymentNotificationRecorder(after time.Time) *obsDeploymentNotificationRecorder {
	return &obsDeploymentNotificationRecorder{after: after, channels: map[obsDeploymentNotificationKey]obsDeploymentNotificationChannel{}, history: obsDeploymentNotificationHistory{
		Scope: "observed_probe_notification_submissions", ItemLimit: api.ObsPostDeployNotificationEventsMax,
		ByteLimit: api.ObsPostDeployNotificationHistoryBytesMax, TrackedChannelLimit: api.ObsServingProbeNotificationChannelsMax,
		PreviewLimit: api.ObsServingProbeNotificationItemsMax, SourceState: "unknown", SourceReason: "awaiting_evidence",
		Events: []obsDeploymentNotificationEvent{},
	}}
}

func (r *obsDeploymentNotificationRecorder) record(item api.ObsServingProbeNotification, at time.Time, attempt int) {
	if item.State == "last_submission_failed" {
		r.history.HadSubmissionFailures = true
	}
	key := obsDeploymentNotificationKey{item.Node, item.Channel}
	previous, seen := r.channels[key]
	if !seen && len(r.channels) == api.ObsServingProbeNotificationChannelsMax {
		r.history.DroppedChannelObservations++
		return
	}
	next := projectObsDeploymentNotificationEvent(item, at, attempt, r.after)
	if item.State == "last_submission_failed" {
		if previous.failureAt == nil || item.LastAttemptAt.After(*previous.failureAt) {
			previous.failureAt = obsDeploymentEvidencePtr(item.LastAttemptAt)
		}
		previous.unresolved = true
	} else if item.State == "last_submission_succeeded" && previous.unresolved && item.LastAttemptAt.After(*previous.failureAt) {
		next.LaterSuccessAfterFailure, previous.unresolved = true, false
	}
	next.LatestObservedFailureAt = obsDeploymentEvidencePtr(previous.failureAt)
	next.UnresolvedObservedFailure = previous.unresolved
	if !seen || !sameObsDeploymentNotification(previous.last, next) {
		r.append(next)
	}
	previous.last = next
	r.channels[key] = previous
}

func sameObsDeploymentNotification(a, b obsDeploymentNotificationEvent) bool {
	if a.State != b.State || a.Reason != b.Reason || a.EvidenceReason != b.EvidenceReason || a.FailureCode != b.FailureCode || a.UnresolvedObservedFailure != b.UnresolvedObservedFailure || !sameObsDeploymentOptionalBool(a.AttemptAtOrAfterBoundary, b.AttemptAtOrAfterBoundary) {
		return false
	}
	return a.State != "last_submission_failed" || a.LastAttemptAt.Equal(*b.LastAttemptAt)
}

func sameObsDeploymentOptionalBool(a, b *bool) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (r *obsDeploymentNotificationRecorder) append(event obsDeploymentNotificationEvent) {
	bytes := obsDeploymentNotificationEventBytes(event)
	if bytes > api.ObsPostDeployNotificationHistoryBytesMax {
		r.history.DroppedEvents++
		return
	}
	for len(r.history.Events) >= api.ObsPostDeployNotificationEventsMax || r.history.RetainedBytes+bytes > api.ObsPostDeployNotificationHistoryBytesMax {
		r.history.RetainedBytes -= obsDeploymentNotificationEventBytes(r.history.Events[0])
		r.history.Events = slices.Delete(r.history.Events, 0, 1)
		r.history.DroppedEvents++
	}
	r.history.Events = append(r.history.Events, event)
	r.history.RetainedBytes += bytes
}

func obsDeploymentNotificationEventBytes(event obsDeploymentNotificationEvent) int {
	body, err := json.MarshalIndent(event, "        ", "  ")
	if err != nil {
		return api.ObsPostDeployNotificationHistoryBytesMax + 1
	}
	// Charge leading indentation and a separator at the event's report depth.
	return len(body) + len("        ") + 2
}

func (r *obsDeploymentNotificationRecorder) snapshot() *obsDeploymentNotificationHistory {
	out := r.history
	out.Events = slices.Clone(r.history.Events)
	out.TrackedChannels = len(r.channels)
	for _, channel := range r.channels {
		if channel.unresolved {
			out.UnresolvedTrackedFailures++
		}
	}
	out.Truncated = out.DroppedEvents > 0 || out.DroppedChannelObservations > 0 || out.MaxPreviewOmitted > 0
	return &out
}

func writeObsDeploymentNotificationHistory(w io.Writer, history *obsDeploymentNotificationHistory) {
	if history == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "probe_notification_history advisory=true events=%d dropped_events=%d envelope_omitted_events=%d retained_bytes=%d truncated=%t tracked_channels=%d dropped_channel_observations=%d unresolved_tracked_failures=%d had_submission_failures=%t max_preview_omitted=%d incomplete_observations=%d source_state=%s source_reason=%s\n", len(history.Events), history.DroppedEvents, history.EnvelopeOmittedEvents, history.RetainedBytes, history.Truncated, history.TrackedChannels, history.DroppedChannelObservations, history.UnresolvedTrackedFailures, history.HadSubmissionFailures, history.MaxPreviewOmitted, history.IncompleteObservations, history.SourceState, history.SourceReason)
	for _, event := range history.Events {
		boundary := "unknown"
		if event.AttemptAtOrAfterBoundary != nil {
			boundary = strconv.FormatBool(*event.AttemptAtOrAfterBoundary)
		}
		_, _ = fmt.Fprintf(w, "  probe_notification at=%s attempt=%d node=%s channel=%s state=%s reason=%s evidence_reason=%s failure_code=%s evaluated_at=%s last_run_at=%s last_attempt_at=%s last_success_at=%s attempt_at_or_after_boundary=%s latest_observed_failure_at=%s unresolved_observed_failure=%t later_success_after_failure=%t\n", event.At.Format(time.RFC3339Nano), event.Attempt, event.Node, event.Channel, event.State, event.Reason, event.EvidenceReason, event.FailureCode, servingTime(event.EvaluatedAt), servingTime(event.LastRunAt), servingTime(event.LastAttemptAt), servingTime(event.LastSuccessAt), boundary, servingTime(event.LatestObservedFailureAt), event.UnresolvedObservedFailure, event.LaterSuccessAfterFailure)
	}
}
