package mcpmanagement

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type errorGroupView struct {
	Fingerprint   string `json:"fingerprint"`
	ErrorClass    string `json:"error_class"`
	Route         string `json:"route"`
	RouteModified bool   `json:"route_modified"`
	HTTPStatus    int32  `json:"http_status"`
	Count         int64  `json:"count"`
	RequestCount  int64  `json:"request_count"`
	FirstSeenAt   string `json:"first_seen_at"`
	LastSeenAt    string `json:"last_seen_at"`
}

type errorGroupsView struct {
	App                string           `json:"app"`
	AppID              string           `json:"app_id"`
	Scope              string           `json:"scope"`
	Status             string           `json:"status"`
	GeneratedAt        string           `json:"generated_at"`
	WindowStart        string           `json:"window_start"`
	WindowEnd          string           `json:"window_end"`
	WindowClamped      bool             `json:"window_clamped"`
	WindowBasis        string           `json:"window_basis"`
	WindowBounds       string           `json:"window_bounds"`
	CountsBasis        string           `json:"counts_basis"`
	Groups             []errorGroupView `json:"groups"`
	ReturnedCount      int              `json:"returned_group_count"`
	Limit              int              `json:"limit"`
	PageComplete       bool             `json:"page_complete"`
	NextCursor         string           `json:"next_cursor,omitempty"`
	NextArguments      *errorGroupsArgs `json:"next_arguments,omitempty"`
	Filters            errorGroupsArgs  `json:"filters"`
	TextTruncated      bool             `json:"text_truncated"`
	SourceCompleteness string           `json:"source_completeness"`
	Freshness          string           `json:"freshness"`
	Order              string           `json:"order"`
	Limitations        string           `json:"limitations"`
}

func validErrorProperties(class string, status int32) bool {
	if status < 400 || status > 599 {
		return false
	}
	switch class {
	case "db_timeout", "stripe_timeout", "null_pointer", "invalid_json", "wake_failed", "upstream_5xx", "unhandled", "client_error":
		return true
	}
	return false
}

func validateErrorGroupWindow(slug, appID string, opts api.AppErrorsSummaryOptions, cursor errorPageCursor, n api.AppErrorsSummaryResponse) error {
	from, fe := errorTime(n.WindowStart)
	until, ue := errorTime(n.WindowEnd)
	generated, ge := errorTime(n.GeneratedAt)
	wantFrom, _ := errorTime(opts.Since)
	wantUntil, _ := errorTime(opts.Until)
	if n.AppID != appID || n.AppSlug != slug || !fe || !ue || !ge || !from.Before(until) || until.After(generated) || !until.Equal(wantUntil) || from.Before(wantFrom) || from.After(wantFrom) != n.WindowClamped || until.Sub(from) > time.Duration(api.AppErrorsWindowMaxHours)*time.Hour {
		return invalid("invalid_response", "native error summary changed app identity or selection/window metadata")
	}
	if cursor.Native != "" {
		pinnedFrom, _ := errorTime(cursor.WindowStart)
		pinnedUntil, _ := errorTime(cursor.WindowEnd)
		if !from.Equal(pinnedFrom) || !until.Equal(pinnedUntil) {
			return invalid("invalid_response", "native error summary continuation changed its effective window; start a new first page")
		}
	}
	return nil
}

func (m *management) projectErrorGroups(slug, appID string, opts api.AppErrorsSummaryOptions, cursor errorPageCursor, n api.AppErrorsSummaryResponse) (errorGroupsView, error) {
	v := errorGroupsView{App: slug, AppID: appID, Scope: "app_wide", Status: "observed", GeneratedAt: n.GeneratedAt, WindowStart: n.WindowStart, WindowEnd: n.WindowEnd, WindowClamped: n.WindowClamped,
		WindowBasis: "group_last_seen_at", WindowBounds: "inclusive", CountsBasis: "native_group_history_counters", Groups: []errorGroupView{}, Limit: opts.Limit, PageComplete: n.NextCursor == "", SourceCompleteness: "unknown", Freshness: "unknown", Order: "count_desc_last_seen_desc_fingerprint_desc",
		Filters: errorGroupsArgs{App: slug, Since: opts.Since, Until: opts.Until, Limit: opts.Limit}, Limitations: errorTriageLimitations}
	if err := validateErrorGroupWindow(slug, appID, opts, cursor, n); err != nil {
		return v, err
	}
	if n.Limit != opts.Limit || len(n.Items) > opts.Limit || n.NextCursor != "" && len(n.Items) != opts.Limit {
		return v, invalid("invalid_response", "native error summary has inconsistent page size or continuation")
	}
	seen := map[string]bool{}
	for i, row := range n.Items {
		if m.clean(row.Fingerprint) != row.Fingerprint {
			return v, invalid("invalid_response", "native fingerprint cannot be returned unchanged")
		}
		if err := validateErrorGroupRow(row, n); err != nil {
			return v, err
		}
		if seen[row.Fingerprint] || i > 0 && !errorGroupAfter(n.Items[i-1], row) {
			return v, invalid("invalid_response", "native error summary duplicated or reordered groups")
		}
		seen[row.Fingerprint] = true
		text := func(s string) string { return m.requestText(s, &v.TextTruncated) }
		route := requestRoute(row.Route, text)
		v.Groups = append(v.Groups, errorGroupView{row.Fingerprint, row.ErrorClass, route, route != row.Route, row.HTTPStatus, row.Count, row.RequestCount, row.FirstSeenAt, row.LastSeenAt})
	}
	v.ReturnedCount = len(v.Groups)
	if len(v.Groups) == 0 {
		v.Status = "no_returned_groups"
	}
	cursor.AppID, cursor.WindowStart, cursor.WindowEnd = appID, n.WindowStart, n.WindowEnd
	next, err := m.writeErrorCursor(cursor, n.NextCursor)
	if err != nil {
		return v, err
	}
	v.NextCursor = next
	if next != "" {
		args := v.Filters
		args.Cursor = next
		v.NextArguments = &args
	}
	return v, nil
}

func validateErrorGroupRow(row api.AppErrorSummaryItem, n api.AppErrorsSummaryResponse) error {
	first, fe := errorTime(row.FirstSeenAt)
	last, le := errorTime(row.LastSeenAt)
	from, _ := errorTime(n.WindowStart)
	until, _ := errorTime(n.WindowEnd)
	if !validErrorFingerprint(row.Fingerprint) || !validErrorProperties(row.ErrorClass, row.HTTPStatus) || row.Count < 1 || row.RequestCount < 0 || !fe || !le || first.After(last) || last.Before(from) || last.After(until) {
		return invalid("invalid_response", "native error group has invalid identifiers, counts, classification or timestamps")
	}
	return nil
}

func errorGroupAfter(previous, row api.AppErrorSummaryItem) bool {
	if previous.Count != row.Count {
		return previous.Count > row.Count
	}
	a, _ := errorTime(previous.LastSeenAt)
	b, _ := errorTime(row.LastSeenAt)
	return a.After(b) || a.Equal(b) && previous.Fingerprint > row.Fingerprint
}

const errorTriageLimitations = "App-wide stored error-group/request observations, not all failures or proof of health. Summary windows select inclusive group last_seen_at, not individual error/request times. Count is the native issue/merge counter; request_count is the native linked-request counter. Both describe stored group history, not window totals, distinct incidents or current retained/page rows; first_seen_at may predate the selected window. Do not derive error rates, count ratios, health or deployment causality. Request drill-down is independent retained fingerprint history without summary time filtering; native retention/cardinality/dedupe limits apply. Page_complete only means no native continuation hint, not complete capture; a full page can have an empty terminal continuation. Pagination is live, not a snapshot; concurrent updates can move groups between pages. Copy next_arguments unchanged; the bounded context envelope preserves the native cursor and selection but grants no authority. Route labels can be modified and collide; never manufacture selectors from them. Public request lookup identifiers are unchanged; debugger evidence availability/retention is independent and not checked. Request ID-only or missing telemetry does not negate a recorded error. No messages, samples, headers, bodies, raw spans/SQL, instance/node/region or deployment/commit/image payloads are exposed. No automatic request evidence reads, probes, replay, collection, model requests or writes. Diagnostic consent grants no additional environment/log/write access."

const errorTriageGuidance = "Use gregale_list_error_groups to find recurring native error fingerprints, then gregale_list_error_requests for one exact unchanged fingerprint. Supply RFC3339 since/until for inclusive group last-seen selection; the bridge resolves defaults once, normalizes to UTC/native microsecond precision and returns explicit filters/next_arguments. Preserve native window/clamp metadata, query generated_at and unknown capture completeness/freshness. Count is the native issue/merge counter and request_count the linked-request counter across stored group history, not selected-window totals, distinct incidents, retained request rows or error-rate denominators. First-seen may predate the selected window. Fingerprints are exact lowercase SHA-256 identifiers, never rewritten labels. Preserve native order, page size and context-bound next_arguments unchanged; page_complete indicates no continuation hint, not all traffic. Native full-page hints can lead to an empty terminal page; live updates mean pagination is not an atomic snapshot. Drill-down selects independent retained fingerprint history without since/until, so do not claim summary-window cohort equivalence or add unsupported filters. Copy a returned public lookup_identifier unchanged to gregale_get_request or gregale_get_request_evidence for the same app only as an explicit independent read. Its evidence_availability not_checked means telemetry can be ID-only or missing under separate native retention/entitlement; this does not disprove a recorded error. Modified route labels can collide and must not become selectors. Do not infer deployment ownership, release order, causal explanations or health from empty results. Never request sample messages, stacks, headers, bodies, SQL, infrastructure/consumer identities or broader permissions. The tools and this recipe fetch no request examples automatically, probe, collect, replay, acknowledge/resolve errors, call a model or write platform state."
