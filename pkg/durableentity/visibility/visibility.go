// Package visibility interprets metadata-only observations. It never performs recovery.
package visibility

import (
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type Finding struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}
type Report struct {
	ObservedAt   time.Time                        `json:"observed_at"`
	Inspection   api.DurableEntityInspectResponse `json:"inspection"`
	AlarmStatus  string                           `json:"alarm_status"`
	OutboxStatus string                           `json:"outbox_status"`
	Findings     []Finding                        `json:"findings"`
	Limitations  []string                         `json:"limitations"`
}

// Summarize uses the caller's clock for due/backoff labels, not proof of worker progress.
func Summarize(in api.DurableEntityInspectResponse, now time.Time) Report {
	out := Report{ObservedAt: now.UTC(), Inspection: in, AlarmStatus: "none", OutboxStatus: "empty", Findings: []Finding{}, Limitations: []string{
		"Metadata observation; ownership and lease status are not exposed.",
		"Replay receipts and application state are not exposed; retain invocation IDs and results.",
		"Head delivery is a later transport observation, not atomic with entity inspection; unknown does not mean never accepted.",
	}}
	add := func(code, detail string) { out.Findings = append(out.Findings, Finding{Code: code, Detail: detail}) }
	if !in.StateCommitted {
		add("state_uncommitted", "No business transition has committed in this scope.")
	}
	if in.Alarm.AlarmAt != nil {
		switch {
		case in.Alarm.Exhausted:
			out.AlarmStatus = "exhausted"
			add("alarm_exhausted", "Alarm attempt budget is exhausted. Inspect the handler failure before an explicit fenced retry.")
		case in.Alarm.AlarmAt.After(now):
			out.AlarmStatus = "scheduled"
		case in.Alarm.NextAttemptAt != nil && in.Alarm.NextAttemptAt.After(now):
			out.AlarmStatus = "backoff"
		default:
			out.AlarmStatus = "due"
			add("alarm_due", "Alarm is due by the local clock; this alone does not establish a stalled worker.")
		}
	}
	if in.Outbox.Pending > 0 {
		switch {
		case in.Outbox.Exhausted:
			out.OutboxStatus = "exhausted"
			add("outbox_exhausted", "Outbox head attempt budget is exhausted. Check webhook configuration and retained delivery history before an explicit fenced retry.")
		case in.Outbox.NextAttemptAt != nil && in.Outbox.NextAttemptAt.After(now):
			out.OutboxStatus = "backoff"
		default:
			out.OutboxStatus = "pending"
		}
		status := "unknown"
		if in.Outbox.HeadDelivery != nil {
			status = in.Outbox.HeadDelivery.Status
		}
		switch status {
		case "unknown", "":
			add("delivery_unknown", "Head delivery history is unavailable or pruned; do not infer that no delivery occurred.")
		case "failed", "dead":
			add("delivery_failed", "Retained head delivery reports failure; entity pending work is a separate observation.")
		case "succeeded":
			add("delivery_succeeded_pending", "Retained head delivery succeeded while the entity head remains pending; acceptance and entity acknowledgement are separate steps.")
		case "pending", "in_flight":
		default:
			add("delivery_unrecognized", "Head delivery returned an unrecognized status; treat transport outcome as unknown.")
		}
	}
	return out
}
