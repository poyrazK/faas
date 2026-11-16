package operatorobs

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ServingProbeNotificationEvidenceReasons refines a recorded unknown outcome
// in an optional field so older clients retain the coarse reason contract.
func ServingProbeNotificationEvidenceReasons() []string {
	return []string{"no_submission_yet", "telemetry_missing", "telemetry_invalid", "source_unavailable"}
}

func notificationRunCurrent(node string, age float64, frame notificationSamples, now time.Time, evaluation *time.Time) (time.Time, bool) {
	run, _, reason := frame.read(notificationKey{probeRun, node, ""}, now, evaluation)
	at, valid := notificationDataClock(run, now)
	return at, reason == "" && valid && float64(now.UnixMilli()-at.UnixMilli())/1000 <= age && !at.After(evaluation.Add(api.ObsHealthClockSkewTolerance))
}

func assessUnknownServingProbeNotification(node, channel string, age float64, frames []notificationSamples, now time.Time, evaluation *time.Time) api.ObsServingProbeNotification {
	out := api.ObsServingProbeNotification{Node: node, Channel: channel, State: "unknown", Reason: "recording_unknown", EvaluatedAt: evaluation}
	status, _, reason := frames[3].read(notificationKey{notificationRecording + "evidence_status", node, channel}, now, evaluation)
	if reason == "recording_missing" {
		// Older rules retain the coarse unknown outcome without inventing a cause.
		return out
	}
	if reason != "" {
		out.Reason, out.EvaluatedAt = reason, nil
		return out
	}
	reasons := ServingProbeNotificationEvidenceReasons()
	if status < 1 || status > float64(len(reasons)) || status != math.Trunc(status) {
		out.Reason, out.EvaluatedAt = "metadata_inconsistent", nil
		return out
	}
	out.EvidenceReason = reasons[int(status)-1]
	if status != 4 {
		if _, current := notificationRunCurrent(node, age, frames[0], now, evaluation); !current {
			out.Reason, out.EvidenceReason, out.EvaluatedAt = "source_unavailable", "", nil
		}
	}
	return out
}
