package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func fitObsDeploymentReport(report obsDeploymentReport) obsDeploymentReport {
	return fitObsDeploymentReportWithin(report, api.ObsIncidentBundleResponseMaxBytes)
}

// Current sources, check transitions, summaries and stability diagnostics take
// priority over detail. Exhaust advisory build previews, advisory history, then
// qualification events and oldest primary fault snapshots from report copies.
func fitObsDeploymentReportWithin(report obsDeploymentReport, limit int) obsDeploymentReport {
	report = fitObsDeploymentRuntimeBuildsWithin(report, limit)
	report = fitObsDeploymentNotificationHistoryWithin(report, limit)
	report = fitObsDeploymentEvidenceHistoryWithin(report, limit)
	if report.Timeline == nil {
		return report
	}
	timeline := *report.Timeline
	timeline.Transitions = slices.Clone(timeline.Transitions)
	if timeline.ProbeEvidence != nil {
		retention := *timeline.ProbeEvidence
		timeline.ProbeEvidence = &retention
	}
	if timeline.NotificationEvidence != nil {
		retention := *timeline.NotificationEvidence
		timeline.NotificationEvidence = &retention
	}
	report.Timeline = &timeline
	for {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			return report
		}
		if !omitOldestObsDeploymentFaultSnapshot(&timeline) {
			return report
		}
	}
}

func omitOldestObsDeploymentFaultSnapshot(timeline *obsDeploymentTimeline) bool {
	for i, transition := range timeline.Transitions {
		if capture := transition.ProbeFailures; capture != nil && timeline.ProbeEvidence != nil {
			accountObsDeploymentEnvelopeOmission(timeline.ProbeEvidence, len(capture.Items), obsDeploymentProbeEvidenceBytes(transition, capture))
			timeline.Transitions[i].ProbeFailures = nil
			return true
		}
		if capture := transition.NotificationFailures; capture != nil && timeline.NotificationEvidence != nil {
			accountObsDeploymentEnvelopeOmission(timeline.NotificationEvidence, len(capture.Items), obsDeploymentNotificationEvidenceBytes(transition, capture))
			timeline.Transitions[i].NotificationFailures = nil
			return true
		}
	}
	return false
}

func accountObsDeploymentEnvelopeOmission(retention *obsDeploymentProbeEvidenceRetention, items, bytes int) {
	retention.RetainedItems -= items
	retention.RetainedBytes -= bytes
	retention.EnvelopeOmittedItems += items
	retention.EnvelopeOmittedSnapshots++
	retention.Truncated = true
}

func writeObsDeploymentEnvelopeOmissions(w io.Writer, label string, retention *obsDeploymentProbeEvidenceRetention) {
	if retention.EnvelopeOmittedSnapshots > 0 {
		_, _ = fmt.Fprintf(w, "  %s envelope_omitted_items=%d envelope_omitted_snapshots=%d\n", label, retention.EnvelopeOmittedItems, retention.EnvelopeOmittedSnapshots)
	}
}
