// adr: 1633
package mcpmanagement

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const errorSince = "2026-10-16T10:00:00Z"
const errorUntil = "2026-10-16T11:00:00Z"
const errorRequestID = "019a0000-0000-7000-8000-000000000001"
const errorNativeGroupsNext = "native-error-groups-page-2=="
const errorNativeRequestsNext = "native-error-requests-page-2=="

func errorFixtureFingerprint() string { return strings.Repeat("b", 64) }

func errorGroupsFixture(appID, cursor string, limit int, empty bool) api.AppErrorsSummaryResponse {
	n := api.AppErrorsSummaryResponse{AppID: appID, AppSlug: "alpha", GeneratedAt: "2026-10-16T11:00:01Z", WindowStart: errorSince, WindowEnd: errorUntil, Limit: limit,
		Items: []api.AppErrorSummaryItem{{Fingerprint: errorFixtureFingerprint(), ErrorClass: "upstream_5xx", Route: "/orders?excluded-query", HTTPStatus: 503,
			Count: 7, RequestCount: 10, FirstSeenAt: "2026-10-01T10:00:00Z", LastSeenAt: errorUntil, SampleMessage: "excluded-message " + testToken, LastInstanceID: "excluded-instance", LastNodeID: "excluded-node", LastRegion: "excluded-region", LastCommitSHA: "excluded-commit", LastDeploymentTag: "excluded-tag", LastDeploymentCreatedAt: "excluded-created", LastImageDigest: "excluded-image"}}}
	if empty || cursor != "" {
		n.Items = nil
	} else if limit == 1 {
		n.NextCursor = errorNativeGroupsNext
	} else {
		row := n.Items[0]
		row.Fingerprint, row.Count, row.RequestCount, row.Route = strings.Repeat("a", 64), 3, 2, "/other"
		n.Items = append(n.Items, row)
	}
	return n
}

func errorRequestsFixture(cursor string, limit int) api.AppErrorRequestsResponse {
	n := api.AppErrorRequestsResponse{Fingerprint: errorFixtureFingerprint(), ErrorClass: "upstream_5xx", Route: "/orders?excluded-query", HTTPStatus: 503,
		Requests: []api.AppErrorRequestItem{{RequestID: errorRequestID, ReceivedAt: "2026-10-15T08:00:00Z", Route: "/orders?excluded-query", HTTPStatus: 503, ErrorClass: "upstream_5xx",
			SampleMessage: "excluded-message " + testToken, DeploymentID: "excluded-deployment", InstanceID: "excluded-instance", NodeID: "excluded-node", Region: "excluded-region", CommitSHA: "excluded-commit", DeploymentTag: "excluded-tag", DeploymentCreatedAt: "excluded-created", ImageDigest: "excluded-image"}}}
	if cursor != "" {
		return api.AppErrorRequestsResponse{Fingerprint: errorFixtureFingerprint()}
	}
	if limit == 1 {
		n.NextCursor = errorNativeRequestsNext
	}
	return n
}

func TestErrorTriageConsentProjectionAndPagination(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "allowlist_denied"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var groups, requests atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("error triage wrote native state")
						w.WriteHeader(405)
						return
					}
					q := r.URL.Query()
					switch r.URL.Path {
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/apps/alpha/errors/summary":
						groups.Add(1)
						if q.Get("since") != errorSince || q.Get("until") != errorUntil || q.Get("limit") != "1" || groups.Load() == 2 && q.Get("cursor") != errorNativeGroupsNext || groups.Load() == 1 && q.Get("cursor") != "" {
							t.Error("native summary selector/cursor was changed")
						}
						_ = json.NewEncoder(w).Encode(errorGroupsFixture("app-1", q.Get("cursor"), 1, false))
					case "/v1/apps/alpha/errors/" + errorFixtureFingerprint():
						requests.Add(1)
						if q.Get("limit") != "1" || q.Get("since") != "" || q.Get("until") != "" || requests.Load() == 2 && q.Get("cursor") != errorNativeRequestsNext || requests.Load() == 1 && q.Get("cursor") != "" {
							t.Error("native fingerprint cursor changed or unsupported window forwarded")
						}
						_ = json.NewEncoder(w).Encode(errorRequestsFixture(q.Get("cursor"), 1))
					default:
						t.Errorf("unexpected read or automatic enrichment %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer b.Close()
				opts := Options{APIBase: b.URL}
				if mode != "local" && mode != "allowlist_denied" {
					opts.Environments = &EnvironmentConsent{RequestReadApps: map[string]string{}}
					if mode == "approved" {
						opts.Environments.RequestReadApps["alpha"] = "app-1"
					} else if mode == "replaced" {
						opts.Environments.RequestReadApps["alpha"] = "old-app"
					}
				}
				if mode == "allowlist_denied" {
					opts.AllowedApps = map[string]string{"other": "other-app"}
				}
				denied := mode == "denied" || mode == "replaced" || mode == "allowlist_denied"
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				g := callTool(t, ctx, cs, "gregale_list_error_groups", errorGroupsArgs{App: "alpha", Since: errorSince, Until: errorUntil, Limit: 1}, denied)
				r := callTool(t, ctx, cs, "gregale_list_error_requests", errorRequestsArgs{App: "alpha", Fingerprint: errorFixtureFingerprint(), Limit: 1}, denied)
				if denied {
					if groups.Load() != 0 || requests.Load() != 0 || mode != "allowlist_denied" && (string(g["code"]) != `"request_access_denied"` || string(r["code"]) != `"request_access_denied"`) {
						t.Fatal("error reads bypassed consent")
					}
					return
				}
				for _, out := range []map[string]json.RawMessage{g, r} {
					encoded, _ := json.Marshal(out)
					for _, marker := range []string{"excluded-", "sample_message", `"deployment_id"`, `"instance_id"`, `"node_id"`, `"headers"`} {
						if strings.Contains(string(encoded), marker) {
							t.Errorf("excluded error data leaked: %s", marker)
						}
					}
					if string(out["scope"]) != `"app_wide"` || string(out["source_completeness"]) != `"unknown"` || string(out["page_complete"]) != "false" {
						t.Fatal("native paging/capture limits lost")
					}
				}
				var gs []errorGroupView
				var rs []errorRequestView
				_ = json.Unmarshal(g["groups"], &gs)
				_ = json.Unmarshal(r["requests"], &rs)
				if len(gs) != 1 || gs[0].Count != 7 || gs[0].RequestCount != 10 || gs[0].FirstSeenAt != "2026-10-01T10:00:00Z" || !gs[0].RouteModified || gs[0].Route != "/orders" || string(g["window_bounds"]) != `"inclusive"` || string(g["counts_basis"]) != `"native_group_history_counters"` {
					t.Fatal("group history was reinterpreted as window totals")
				}
				if len(rs) != 1 || rs[0].LookupIdentifier != errorRequestID || rs[0].ReceivedAt != "2026-10-15T08:00:00Z" || string(r["evidence_availability"]) != `"not_checked"` || string(r["selection_basis"]) != `"retained_fingerprint_history_without_summary_window_filter"` || r["window_start"] != nil {
					t.Fatal("request lookup or independent retention semantics lost")
				}
				var ga errorGroupsArgs
				var ra errorRequestsArgs
				_ = json.Unmarshal(g["next_arguments"], &ga)
				_ = json.Unmarshal(r["next_arguments"], &ra)
				if ga.Cursor == errorNativeGroupsNext || ra.Cursor == errorNativeRequestsNext || ga.Since != errorSince || ga.Until != errorUntil || ga.Limit != 1 || ra.Fingerprint != errorFixtureFingerprint() {
					t.Fatal("continuation context was not pinned")
				}
				lastG := callTool(t, ctx, cs, "gregale_list_error_groups", ga, false)
				lastR := callTool(t, ctx, cs, "gregale_list_error_requests", ra, false)
				if string(lastG["groups"]) != "[]" || string(lastR["requests"]) != "[]" || string(lastG["page_complete"]) != "true" || string(lastR["header_status"]) != `"not_reported"` || lastG["next_arguments"] != nil || lastR["next_arguments"] != nil || groups.Load() != 2 || requests.Load() != 2 {
					t.Fatal("full-page empty terminal continuation was mishandled")
				}
				ga.Limit, ra.Fingerprint = 2, strings.Repeat("a", 64)
				callTool(t, ctx, cs, "gregale_list_error_groups", ga, true)
				callTool(t, ctx, cs, "gregale_list_error_requests", ra, true)
				if groups.Load() != 2 || requests.Load() != 2 {
					t.Fatal("changed continuation selectors reached diagnostics")
				}
			})
		}
	}
}

func TestErrorTriageSelectionAndCursorValidation(t *testing.T) {
	m := &management{}
	for _, mode := range []string{"default", "offset", "native_precision", "collapsed_precision", "bad_since", "duration", "future", "reversed", "cursor_without_window", "negative_limit", "large_limit", "long_time", "bad_cursor"} {
		t.Run(mode, func(t *testing.T) {
			a := errorGroupsArgs{App: "alpha"}
			switch mode {
			case "offset":
				a.Since, a.Until = "2026-10-16T12:00:00+02:00", "2026-10-16T13:00:00+02:00"
			case "native_precision":
				a.Since, a.Until = "2026-10-16T10:00:00.000000123Z", "2026-10-16T11:00:00.000000999Z"
			case "collapsed_precision":
				a.Since, a.Until = "2026-10-16T10:00:00.000000123Z", "2026-10-16T10:00:00.000000999Z"
			case "bad_since":
				a.Since = "bad-time"
			case "duration":
				a.Since = "1h"
			case "future":
				a.Until = time.Now().Add(24 * time.Hour).Format(time.RFC3339)
			case "reversed":
				a.Since, a.Until = errorUntil, errorSince
			case "cursor_without_window":
				a.Cursor = "opaque"
			case "negative_limit":
				a.Limit = -1
			case "large_limit":
				a.Limit = api.MCPErrorPageMax + 1
			case "long_time":
				a.Since = strings.Repeat("x", api.MCPErrorTimeMaxBytes+1)
			case "bad_cursor":
				a.Since, a.Until, a.Cursor = errorSince, errorUntil, "not-an-envelope"
			}
			opts, _, err := m.errorGroupsSelection(a)
			good := mode == "default" || mode == "offset" || mode == "native_precision"
			if (err == nil) != good {
				t.Fatalf("selection=%+v err=%v", opts, err)
			}
			if mode == "default" {
				from, _ := errorTime(opts.Since)
				until, _ := errorTime(opts.Until)
				if until.Sub(from) != 24*time.Hour || opts.Limit != api.MCPErrorPageDefault {
					t.Fatal("defaults were not resolved once to explicit timestamps")
				}
			}
			if (mode == "offset" || mode == "native_precision") && (opts.Since != errorSince || opts.Until != errorUntil) {
				t.Fatal("timestamp offsets not normalized")
			}
		})
	}
	want := errorPageCursor{Version: 1, Kind: "groups", App: "alpha", Limit: 1, Since: errorSince, Until: errorUntil}
	for _, mode := range []string{"valid", "kind", "app", "identity_missing", "window_missing", "since", "until", "limit", "native_control", "native_secret", "version", "oversized", "unknown_field"} {
		t.Run("cursor/"+mode, func(t *testing.T) {
			c := want
			c.AppID, c.WindowStart, c.WindowEnd, c.Native = "app-1", errorSince, errorUntil, errorNativeGroupsNext
			switch mode {
			case "kind":
				c.Kind = "requests"
			case "app":
				c.App = "other"
			case "identity_missing":
				c.AppID = ""
			case "window_missing":
				c.WindowStart = ""
			case "since":
				c.Since = "2026-10-15T10:00:00Z"
			case "until":
				c.Until = "2026-10-16T10:00:00Z"
			case "limit":
				c.Limit = 2
			case "native_control":
				c.Native = "native\ncursor"
			case "native_secret":
				c.Native = testToken
			case "version":
				c.Version = 2
			case "oversized":
				c.Native = strings.Repeat("x", api.MCPErrorCursorMaxBytes)
			}
			data, _ := json.Marshal(c)
			if mode == "unknown_field" {
				data = append(data[:len(data)-1], []byte(`,"extra":true}`)...)
			}
			raw := "e1." + base64.RawURLEncoding.EncodeToString(data)
			got, err := m.readErrorCursor(raw, want)
			if (err == nil) != (mode == "valid") || mode == "valid" && got.Native != errorNativeGroupsNext {
				t.Fatalf("cursor validation=%+v %v", got, err)
			}
		})
	}
	for _, fp := range []string{"", strings.Repeat("B", 64), strings.Repeat("g", 64), strings.Repeat("b", 63), "../summary", testToken} {
		if validErrorFingerprint(fp) {
			t.Fatal("accepted invalid fingerprint")
		}
	}
}

func TestErrorTriageNativeAndIdentityGuards(t *testing.T) {
	for _, tool := range []string{"gregale_list_error_groups", "gregale_list_error_requests"} {
		for _, mode := range []string{"app_changed", "cursor_app_changed", "plan_gated", "not_found", "native_error", "wrong_identity", "empty", "redacted", "unsafe_cursor", "repeated_cursor", "extra_argument"} {
			t.Run(tool+"/"+mode, func(t *testing.T) {
				var reads atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/apps/alpha" {
						id := "app-1"
						if mode == "app_changed" && reads.Load() > 0 || mode == "cursor_app_changed" {
							id = "replacement"
						}
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
						return
					}
					reads.Add(1)
					if mode == "plan_gated" || mode == "not_found" || mode == "native_error" {
						status := map[string]int{"plan_gated": 402, "not_found": 404, "native_error": 503}[mode]
						api.WriteProblem(w, api.NewProblem(status, "native_error_triage", "Unavailable", "native read unavailable"))
						return
					}
					if tool == "gregale_list_error_groups" {
						n := errorGroupsFixture("app-1", "", 1, mode == "empty")
						switch mode {
						case "wrong_identity":
							n.AppID = "foreign"
						case "redacted":
							n.Items[0].Route = "/" + testToken + strings.Repeat("界", 256) + "?excluded-query"
						case "unsafe_cursor":
							n.NextCursor = testToken
						case "repeated_cursor":
							n.NextCursor = errorNativeGroupsNext
						}
						_ = json.NewEncoder(w).Encode(n)
					} else {
						n := errorRequestsFixture("", 1)
						switch mode {
						case "wrong_identity":
							n.Fingerprint = strings.Repeat("a", 64)
						case "empty":
							n = api.AppErrorRequestsResponse{Fingerprint: errorFixtureFingerprint()}
						case "redacted":
							n.Route = "/" + testToken + strings.Repeat("界", 256)
							n.Requests[0].Route = n.Route
						case "unsafe_cursor":
							n.NextCursor = testToken
						case "repeated_cursor":
							n.NextCursor = errorNativeRequestsNext
						}
						_ = json.NewEncoder(w).Encode(n)
					}
				}))
				defer b.Close()
				cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
				args := map[string]any{"app": "alpha", "limit": 1}
				c := errorPageCursor{Version: 1, App: "alpha", AppID: "app-1", Limit: 1}
				if tool == "gregale_list_error_groups" {
					args["since"], args["until"] = errorSince, errorUntil
					c.Kind, c.Since, c.Until, c.WindowStart, c.WindowEnd = "groups", errorSince, errorUntil, errorSince, errorUntil
				} else {
					args["fingerprint"] = errorFixtureFingerprint()
					c.Kind, c.Fingerprint = "requests", errorFixtureFingerprint()
				}
				if mode == "repeated_cursor" || mode == "cursor_app_changed" {
					native := errorNativeGroupsNext
					if tool == "gregale_list_error_requests" {
						native = errorNativeRequestsNext
					}
					wrapped, err := (&management{}).writeErrorCursor(c, native)
					if err != nil {
						t.Fatal(err)
					}
					args["cursor"] = wrapped
				}
				if mode == "extra_argument" {
					args["on_behalf_of"] = "other-account"
					r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
					if err != nil || !r.IsError || reads.Load() != 0 {
						t.Fatal("unreviewed argument bypassed schema")
					}
					return
				}
				good := mode == "redacted" || mode == "empty" && tool == "gregale_list_error_groups"
				out := callTool(t, ctx, cs, tool, args, !good)
				if mode == "cursor_app_changed" && reads.Load() != 0 || mode != "cursor_app_changed" && reads.Load() != 1 {
					t.Fatal("identity pin bypassed or native fallback attempted")
				}
				if !good && (out["groups"] != nil || out["requests"] != nil) {
					t.Fatal("withheld data leaked")
				}
				if mode == "redacted" && string(out["text_truncated"]) != "true" {
					t.Fatal("text limit not reported")
				}
				if mode == "empty" && good && (string(out["status"]) != `"no_returned_groups"` || string(out["groups"]) != "[]") {
					t.Fatal("empty page claimed health")
				}
			})
		}
	}
}
