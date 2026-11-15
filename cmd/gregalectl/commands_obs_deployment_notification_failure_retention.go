package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (r *obsDeploymentTimelineRecorder) retainNotificationFailures(transition obsDeploymentTransition, failures obsDeploymentNotificationFailureSet) *obsDeploymentNotificationFailureSnapshot {
	if failures.total == 0 {
		return nil
	}
	count := min(len(failures.preview), api.ObsPostDeployNotificationEvidenceItemsMax-r.notificationEvidence.RetainedItems)
	for count >= 0 {
		capture := &obsDeploymentNotificationFailureSnapshot{CauseSHA256: failures.fingerprint, Total: failures.total, Omitted: failures.total - count, Truncated: failures.total > count, Items: cloneObsDeploymentNotificationFailures(failures.preview[:count])}
		bytes := obsDeploymentNotificationEvidenceBytes(transition, capture)
		if bytes <= api.ObsPostDeployNotificationEvidenceBytesMax-r.notificationEvidence.RetainedBytes {
			r.notificationEvidence.RetainedItems += count
			r.notificationEvidence.RetainedBytes += bytes
			r.notificationEvidence.DroppedItems += capture.Omitted
			return capture
		}
		count--
	}
	r.notificationEvidence.DroppedItems += failures.total
	r.notificationEvidence.DroppedSnapshots++
	return nil
}

func cloneObsDeploymentNotificationFailures(items []obsDeploymentNotificationFailure) []obsDeploymentNotificationFailure {
	out := make([]obsDeploymentNotificationFailure, len(items))
	for i, item := range items {
		out[i] = item
		out[i].Expected, out[i].Threshold = obsDeploymentEvidencePtr(item.Expected), obsDeploymentEvidencePtr(item.Threshold)
		out[i].Signals = slices.Clone(item.Signals)
		for j, signal := range item.Signals {
			out[i].Signals[j].Value, out[i].Signals[j].ObservedAt = obsDeploymentEvidencePtr(signal.Value), obsDeploymentEvidencePtr(signal.ObservedAt)
		}
	}
	return out
}

func obsDeploymentNotificationEvidenceBytes(transition obsDeploymentTransition, evidence *obsDeploymentNotificationFailureSnapshot) int {
	transition.NotificationFailures = evidence
	with, err := json.MarshalIndent(transition, "      ", "  ")
	if err != nil {
		return api.ObsPostDeployNotificationEvidenceBytesMax + 1
	}
	transition.NotificationFailures = nil
	without, err := json.MarshalIndent(transition, "      ", "  ")
	if err != nil {
		return api.ObsPostDeployNotificationEvidenceBytesMax + 1
	}
	return len(with) - len(without)
}

func writeObsDeploymentNotificationEvidence(w io.Writer, history *obsDeploymentTimeline) {
	if retention := history.NotificationEvidence; retention != nil {
		_, _ = fmt.Fprintf(w, "notification_evidence retained_items=%d retained_bytes=%d dropped_items=%d dropped_snapshots=%d truncated=%t\n", retention.RetainedItems, retention.RetainedBytes, retention.DroppedItems, retention.DroppedSnapshots, retention.Truncated)
		writeObsDeploymentEnvelopeOmissions(w, "notification_evidence", retention)
	}
	for _, transition := range history.Transitions {
		capture := transition.NotificationFailures
		if capture == nil {
			continue
		}
		_, _ = fmt.Fprintf(w, "  notification_failures at=%s attempt=%d check=%s total=%d retained=%d omitted=%d truncated=%t cause_sha256=%s\n", transition.At.Format(time.RFC3339Nano), transition.Attempt, transition.Check, capture.Total, len(capture.Items), capture.Omitted, capture.Truncated, capture.CauseSHA256)
		for _, item := range capture.Items {
			expected := "unknown"
			if item.Expected != nil {
				expected = fmt.Sprintf("%t", *item.Expected)
			}
			_, _ = fmt.Fprintf(w, "    kind=%s name=%s receiver=%s integration=%s reason=%s expected=%s", item.Kind, item.Name, item.Receiver, item.Integration, item.Reason, expected)
			if item.Threshold != nil {
				_, _ = fmt.Fprintf(w, " threshold=%g", *item.Threshold)
			}
			_, _ = fmt.Fprintln(w)
			for _, signal := range item.Signals {
				value := "unknown"
				if signal.Value != nil {
					value = fmt.Sprintf("%g", *signal.Value)
				}
				_, _ = fmt.Fprintf(w, "      signal=%s state=%s reason=%s value=%s observed_at=%s window_seconds=%d max_age_seconds=%d\n", signal.Name, signal.State, signal.Reason, value, servingTime(signal.ObservedAt), signal.WindowSeconds, signal.MaxAgeSeconds)
			}
		}
	}
}
