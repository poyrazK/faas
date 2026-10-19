package mcpmanagement

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type wakeTimelineStageView struct {
	Stage      string `json:"stage"`
	Status     string `json:"status"`
	EventCount int    `json:"event_count"`
}

type wakeTimelineEventView struct {
	At         string                  `json:"at"`
	Kind       string                  `json:"kind"`
	KindStatus string                  `json:"kind_status"`
	Stage      string                  `json:"stage"`
	Details    wakeTimelineDetailsView `json:"details"`
}

type wakeTimelineView struct {
	App                  string                  `json:"app"`
	AppID                string                  `json:"app_id"`
	WakeID               string                  `json:"wake_id"`
	Scope                string                  `json:"scope"`
	Status               string                  `json:"status"`
	RequestedSince       string                  `json:"requested_since,omitempty"`
	SincePrecision       string                  `json:"since_precision"`
	LowerBound           string                  `json:"lower_bound"`
	WindowVerification   string                  `json:"window_verification"`
	Limit                int                     `json:"limit"`
	NativeCount          int                     `json:"native_returned_event_count"`
	ReturnedCount        int                     `json:"returned_event_count"`
	NativeMoreEventsHint bool                    `json:"native_more_events_hint"`
	NativeCursorAt       string                  `json:"native_cursor_at,omitempty"`
	PaginationStatus     string                  `json:"pagination_status"`
	PageComplete         *bool                   `json:"page_complete,omitempty"`
	NextCursor           string                  `json:"next_cursor,omitempty"`
	NextArguments        *wakeTimelineArgs       `json:"next_arguments,omitempty"`
	UnknownEventCount    int                     `json:"unknown_event_count"`
	TextTruncated        bool                    `json:"text_truncated"`
	Order                string                  `json:"order"`
	EventTimeBasis       string                  `json:"event_time_basis"`
	SourceCompleteness   string                  `json:"source_completeness"`
	Freshness            string                  `json:"freshness"`
	StageObservations    []wakeTimelineStageView `json:"stage_observations"`
	Events               []wakeTimelineEventView `json:"events"`
	Limitations          string                  `json:"limitations"`
}

func (m *management) projectWakeTimeline(args wakeTimelineArgs, appID string, n api.WakeTimelineResponse) (wakeTimelineView, error) {
	v := wakeTimelineView{App: args.App, AppID: appID, WakeID: args.WakeID, Scope: "app_wide", Status: "observed", RequestedSince: args.Since, SincePrecision: "native_postgresql_microseconds", LowerBound: "exclusive", WindowVerification: "not_echoed_by_native",
		Limit: args.Limit, NativeCount: len(n.Events), NativeMoreEventsHint: n.NextCursor != "", NativeCursorAt: n.NextCursor, PaginationStatus: "timestamp_only_not_lossless", Order: "native_recorded_at_ascending", EventTimeBasis: "native_event_recorded_at", SourceCompleteness: "unknown", Freshness: "unknown", Events: []wakeTimelineEventView{}, StageObservations: []wakeTimelineStageView{}, Limitations: wakeTimelineGuidance}
	if err := validateWakeTimelineEnvelope(args, appID, n); err != nil {
		return v, err
	}
	if err := m.projectWakePagination(args, appID, n, &v); err != nil {
		return v, err
	}
	counts := map[string]int{}
	for _, row := range n.Events {
		stage := wakeTimelineStage(row.Kind)
		e := wakeTimelineEventView{At: row.At, Kind: row.Kind, KindStatus: "recognized", Stage: stage}
		if stage == "unknown" {
			e.Kind, e.KindStatus = "unrecognized_event", "unrecognized"
			v.UnknownEventCount++
		} else {
			counts[stage]++
		}
		var err error
		e.Details, err = m.projectWakeTimelineDetails(row, stage, &v.TextTruncated)
		if err != nil {
			return v, err
		}
		v.Events = append(v.Events, e)
	}
	for _, stage := range []string{"queue", "admission", "boot", "readiness", "proxy", "parking"} {
		status := "not_returned"
		if counts[stage] > 0 {
			status = "observed"
		}
		v.StageObservations = append(v.StageObservations, wakeTimelineStageView{Stage: stage, Status: status, EventCount: counts[stage]})
	}
	v.ReturnedCount = len(v.Events)
	if len(v.Events) == 0 {
		v.Status = "no_returned_events"
	}
	return v, nil
}

func validateWakeTimelineEnvelope(args wakeTimelineArgs, appID string, n api.WakeTimelineResponse) error {
	fail := invalid("invalid_response", "native wake timeline has inconsistent identity, event bounds or timestamps")
	if n.AppID != appID || n.WakeID != args.WakeID || n.Limit != args.Limit || len(n.Events) > args.Limit {
		return fail
	}
	since, _ := wakeTimelineTime(args.Since)
	var previous time.Time
	for _, row := range n.Events {
		at, ok := wakeTimelineTime(row.At)
		if !ok || at.IsZero() || !at.After(since) || !previous.IsZero() && at.Before(previous) || row.Data["app_id"] != appID || row.Data["wake_id"] != args.WakeID {
			return fail
		}
		previous = at
	}
	if n.NextCursor != "" {
		cursor, ok := wakeTimelineTime(n.NextCursor)
		// Native forge filtering happens after the SQL limit. The hint can
		// precede the last visible event, so it is not an event seek tuple.
		if !ok || !cursor.After(since) || len(n.Events) == 0 {
			return fail
		}
	}
	return nil
}

func wakeTimelineStage(kind string) string {
	switch kind {
	case "wake.queue_accepted":
		return "queue"
	case "wake.admitted":
		return "admission"
	case "wake.boot_started", "wake.boot_observed", "wake.boot_completed", "wake.boot_failed", "wake.restore_breakdown", "wake.cold_boot_breakdown":
		return "boot"
	case "wake.readiness_200":
		return "readiness"
	case "wake.proxy_first_byte", "wake.page_served":
		return "proxy"
	case "wake.park_started", "wake.park_completed", "wake.park_failed":
		return "parking"
	default:
		return "unknown"
	}
}
