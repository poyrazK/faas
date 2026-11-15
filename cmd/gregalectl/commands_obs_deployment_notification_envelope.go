package main

import (
	"encoding/json"
	"slices"
)

const obsDeploymentNotificationPreviewLimitation = "direct_notification_history_limited_to_observed_preview"
const obsDeploymentNotificationEnvelopeLimitation = "direct_notification_history_omitted_report_envelope"

// Advisory events yield space to the existing source snapshots and health
// evidence. Work on copies: fitting a report never mutates the recorder or a
// previously saved report, and does not alter qualification or exit behavior.
func fitObsDeploymentNotificationHistoryWithin(report obsDeploymentReport, limit int) obsDeploymentReport {
	if report.Timeline == nil || report.Timeline.ProbeNotifications == nil {
		return report
	}
	timeline := *report.Timeline
	history := *timeline.ProbeNotifications
	for {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit { // The private file ends with a newline.
			return report
		}
		if len(history.Events) == 0 {
			return omitObsDeploymentNotificationHistory(report, timeline)
		}
		needed, removed := len(body)+1-limit, 0
		for len(history.Events) > 0 && removed < needed {
			bytes := obsDeploymentNotificationEventBytes(history.Events[0])
			history.Events = history.Events[1:]
			history.RetainedBytes -= bytes
			history.EnvelopeOmittedEvents++
			history.Truncated = true
			removed += bytes
		}
		timeline.ProbeNotifications = &history
		report.Timeline = &timeline
	}
}

func omitObsDeploymentNotificationHistory(report obsDeploymentReport, timeline obsDeploymentTimeline) obsDeploymentReport {
	timeline.ProbeNotifications = nil
	report.Timeline = &timeline
	report.Limitations = slices.Clone(report.Limitations)
	for i, limitation := range report.Limitations {
		if limitation == obsDeploymentNotificationPreviewLimitation {
			// This replacement is shorter than the existing limitation. Omitting
			// the optional section therefore cannot grow the original report.
			report.Limitations[i] = obsDeploymentNotificationEnvelopeLimitation
			return report
		}
	}
	report.Limitations = append(report.Limitations, obsDeploymentNotificationEnvelopeLimitation)
	return report
}
