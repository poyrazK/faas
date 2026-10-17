package mcpmanagement

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type wakeHistoryArgs struct {
	App    string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent across environments"`
	Since  string `json:"since,omitempty" jsonschema:"Inclusive RFC3339 start, at most 64 bytes. Default until minus 24h. Normalized to PostgreSQL microseconds. Older APIs filter only their latest 100 instance candidates."`
	Until  string `json:"until,omitempty" jsonschema:"Inclusive RFC3339 end at or before now, at most 64 bytes. Default resolved once to now. Original end remains fixed across keyset pages. Older APIs have whole-second timestamps and can include missing start times."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Newest-first page size 1 to 50; default 20. Native keyset support returns next_arguments; older APIs supply only a bounded output prefix."`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque next_cursor, at most 8192 bytes. Repeat the exact app, original since/until and limit from next_arguments. Requires native keyset support."`
}

func wakeHistoryTime(s string) (time.Time, bool) {
	if !requestFilterText(s, api.MCPWakeHistoryTimeMaxBytes) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func wakeHistorySelection(args wakeHistoryArgs) (api.AppWakeTimelineOptions, int, error) {
	var opts api.AppWakeTimelineOptions
	if err := validateSegment(args.App, "app"); err != nil {
		return opts, 0, err
	}
	now := time.Now().UTC()
	until := now
	if args.Until != "" {
		var ok bool
		until, ok = wakeHistoryTime(args.Until)
		if !ok || until.After(now) {
			return opts, 0, invalid("invalid_argument", "until must be an RFC3339 timestamp at or before now")
		}
	}
	since := until.Add(-24 * time.Hour)
	if args.Since != "" {
		var ok bool
		since, ok = wakeHistoryTime(args.Since)
		if !ok {
			return opts, 0, invalid("invalid_argument", "since must be an RFC3339 timestamp")
		}
	}
	if !since.Before(until) {
		return opts, 0, invalid("invalid_argument", "since must be earlier than until")
	}
	since, until = since.UTC().Truncate(time.Microsecond), until.UTC().Truncate(time.Microsecond)
	if since.IsZero() || until.IsZero() || !since.Before(until) {
		return opts, 0, invalid("invalid_argument", "history window must be nonzero and ordered at PostgreSQL microsecond precision")
	}
	opts.Since, opts.Until = since.Format(time.RFC3339Nano), until.Format(time.RFC3339Nano)
	limit := args.Limit
	if limit == 0 {
		limit = api.MCPWakeHistoryDefault
	}
	if limit < 1 || limit > api.MCPWakeHistoryMax {
		return opts, 0, invalid("invalid_argument", "limit must be between 1 and 50")
	}
	return opts, limit, nil
}

func (m *management) getWakeHistory(ctx context.Context, args wakeHistoryArgs) (any, error) {
	opts, limit, err := wakeHistorySelection(args)
	if err != nil {
		return nil, err
	}
	args.Since, args.Until, args.Limit = opts.Since, opts.Until, limit
	cursor, err := m.readHistoryCursor(args)
	if err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	if cursor.AppID != "" && cursor.AppID != app.ID {
		return nil, invalid("request_access_denied", "app identity changed between history pages")
	}
	n, err := m.client.GetAppWakeHistoryPage(ctx, args.App, api.AppWakeHistoryPageOptions{Since: opts.Since, Until: opts.Until, Limit: limit, Cursor: cursor.Native})
	if err != nil {
		return nil, err
	}
	v, err := m.projectWakeHistory(args.App, app.ID, opts, limit, n)
	if err != nil {
		return nil, err
	}
	if err := m.projectHistoryPagination(args, app.ID, n, &v); err != nil {
		return nil, err
	}
	return m.performanceResult(performanceArgs{App: args.App}, app.ID, v), nil
}

const wakeHistoryGuidance = "Use gregale_get_wake_history for one explicit bounded page of retained instance observations. Defaults resolve once to 24h/now; normalize since/until to UTC/PostgreSQL microseconds and preserve the original bounds. Native keyset support reports keyset_started_at_instance_id_v1 and native_window_echo: app/window filtering precedes limiting, missing start times are excluded, and timestamps retain microseconds. Use exact next_arguments to continue in descending start-time/instance-ID order; the context envelope pins app identity, window and size and preserves the native cursor unchanged. Each page rechecks diagnostic consent and identity; cursors grant no authority. Never auto-page. read_succeeded proves this database read succeeded, not full capture or freshness. Source failures are errors, without fallback or wider queries. Counts, histograms and capacity summaries describe this returned page only. page_complete marks the end of this live retained read, not a frozen snapshot; late/backdated inserts, retention, state and metadata can affect independent pages. Older APIs omit page metadata: latest_candidates_not_pageable retains their latest 100 instance candidates selected before inclusive time filtering, whole-second timestamp overlap compatibility, missing timestamps are unverified and source_status not_reported, without next_arguments. A resumed downgrade returns native_upgrade_required. Preserve counts, requested bounds, window verification and unknown source completeness/freshness; empty output never proves zero wakes or health. Counts describe selected instance records, not unique independent attempts, requests or all wakes; shared wake metadata is not exact instance attribution. At-capacity percentage uses meta-bearing records, including those with unknown capacity; it is not an all-wake saturation rate. A present false capacity bit remains observed false; absent stays unknown. Both queued_count and concurrency_at_admit originate from the native admission concurrency ledger, not waiting queue depth or wait duration. Scalar zero loses presence and remains zero_or_not_reported. Readiness exposes positive native boot/completion durations only; -1/zero remains not_reported and does not prove failure or zero latency. State/method/tier/trigger are bounded untrusted display labels, can collide and never become selectors or causal proof. Use only an unchanged returned wake_id for a separate explicit gregale_get_wake_timeline read; event retention is independent, and history never fetches stages automatically. No request/instance/node/deployment payloads, raw metadata, app URLs, histograms or automatic enrichment are exported. Do not infer restoration failure, resource changes or savings, add readiness to request latency, probe, replay, collect or mutate. App-wide diagnostic consent grants no extra environment/log/write access; prompt retrieval fetches no evidence."
