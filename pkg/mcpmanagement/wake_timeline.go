package mcpmanagement

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type wakeTimelineArgs struct {
	App    string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent across environments"`
	WakeID string `json:"wake_id" jsonschema:"Exact unchanged wake ID from request wake association, wake history or the user; at most 128 bytes. Retained events may be unavailable even when history contains this ID."`
	Since  string `json:"since,omitempty" jsonschema:"Exclusive RFC3339 lower bound, at most 64 bytes, normalized to UTC and PostgreSQL microseconds. Omit to read from the start of retained history. No until. Repeat this original lower bound with next_arguments for keyset continuation."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Native oldest-first event bound from 1 to 100; default 50. Keyset support supplies explicit next_arguments. Older APIs remain bounded compatibility reads without continuation."`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque MCP next_cursor, at most 8192 bytes. Repeat the exact app, wake_id, normalized since and limit in next_arguments. Requires native keyset_v1 support; a legacy timestamp hint is not a cursor."`
}

func (m *management) wakeTimelineSelection(args wakeTimelineArgs) (wakeTimelineArgs, error) {
	if err := validateSegment(args.App, "app"); err != nil {
		return args, err
	}
	if validateSegment(args.WakeID, "wake_id") != nil || len(args.WakeID) > api.MCPRequestIdentifierMaxBytes || m.clean(args.WakeID) != args.WakeID {
		return args, invalid("invalid_argument", "wake_id must be an unchanged safe identifier of at most 128 bytes")
	}
	if args.Since != "" {
		t, ok := wakeTimelineTime(args.Since)
		t = t.UTC().Truncate(time.Microsecond)
		if !ok || t.IsZero() {
			return args, invalid("invalid_argument", "since must be a nonzero RFC3339 timestamp of at most 64 bytes")
		}
		args.Since = t.Format(time.RFC3339Nano)
	}
	if args.Limit == 0 {
		args.Limit = api.MCPWakeTimelineDefault
	}
	if args.Limit < 1 || args.Limit > api.MCPWakeTimelineMax {
		return args, invalid("invalid_argument", "limit must be between 1 and 100")
	}
	if args.Cursor != "" && !m.safeWakeCursor(args.Cursor, api.MCPWakeTimelineCursorMaxBytes) {
		return args, invalid("invalid_argument", "invalid wake timeline continuation")
	}
	return args, nil
}

func wakeTimelineTime(s string) (time.Time, bool) {
	if !requestFilterText(s, api.MCPWakeTimelineTimeMaxBytes) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func (m *management) getWakeTimeline(ctx context.Context, args wakeTimelineArgs) (any, error) {
	args, err := m.wakeTimelineSelection(args)
	if err != nil {
		return nil, err
	}
	cursor, err := m.readWakeCursor(args)
	if err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	if cursor.AppID != "" && cursor.AppID != app.ID {
		return nil, invalid("request_access_denied", "app identity changed between wake pages")
	}
	native, err := m.client.ListWakeTimelinePage(ctx, args.App, args.WakeID, api.WakeTimelinePageOptions{Since: args.Since, Limit: args.Limit, Cursor: cursor.Native})
	if err != nil {
		return nil, err
	}
	v, err := m.projectWakeTimeline(args, app.ID, native)
	if err != nil {
		return nil, err
	}
	return m.performanceResult(performanceArgs{App: args.App}, app.ID, v), nil
}

const wakeTimelineGuidance = "Use gregale_get_wake_timeline only for an exact unchanged wake_id supplied by the user, returned by gregale_get_wake_history or exposed in a validated request.wake association; history and event retention are independent. This is one bounded native oldest-first read, default 50 and maximum 100 events. since is an exclusive RFC3339 lower bound normalized to PostgreSQL microsecond precision, not a lookback alias; omission reads from the start of retained history. There is no until. When pagination_status keyset_at_id_v1 is reported, use returned next_arguments to explicitly continue in native timestamp/event-ID order, preserving app, wake_id, original normalized since and limit. The opaque MCP envelope pins immutable app identity and retains the native cursor unchanged; it grants no authority. page_complete marks only the end of this live retained read, not capture completeness or a snapshot. Late/backdated inserts, retention and canonical changes can affect pages. native_since_echo verifies the normalized lower bound. Older native APIs report timestamp_only_not_lossless and not_echoed_by_native without next_arguments: their timestamp hint lacks an event ID and can skip ties. Never manufacture continuation or advance by epsilon. A resumed read that loses native keyset support returns native_upgrade_required without fallback. Do not auto-page or claim exhaustive history. native_more_events_hint only describes a native continuation hint, not capture completeness. Event timestamps are native recorded times, not necessarily producer phase boundaries. Preserve repeated kinds and tied timestamps; observations may describe multiple requests or instances sharing a wake. Selected producer timestamps, readiness_elapsed_ms, acceptance_to_first_byte_ms, post_wake_proxy_ms and native_call_total_ms are independent observations with different timing boundaries: never subtract, sum or turn them into a causal critical path or queue-wait measurement. Queue acceptance supplies no queue_wait_ms. Raw event payload keys preserve observed zero counts/timings and false capacity; missing/null stays unknown. queued_count and concurrency_at_admit describe the admission concurrency ledger, not waiting queue depth. Unlike wake history's flattened DTO, timeline payloads retain numeric presence. Method/tier/trigger are bounded untrusted display labels; fallback reason is a closed category and unknown remains unrecognized. Unknown kinds retain only a timestamp and unrecognized_event marker; excluded/unknown details and missing stages do not establish healthy operation, failure or current state. Stage observations count this returned page only. Source completeness and freshness remain unknown; no native as_of/effective-window metadata exists. Respect app-wide diagnostic consent, immutable app checks and native plan/MFA/retention errors without wider queries or retries. No raw data, request/instance/node/deployment/snapshot IDs, actor labels, free-text failure messages, healthcheck paths, nested phase/artifact maps, CPU/billing fields, secrets or extra permissions are exported. Do not fetch timeline evidence automatically from history, request explanations or prompt retrieval; do not probe, replay, collect or mutate."
