// adr: 1619
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const discoveryDeploymentID = "11111111-1111-4111-8111-111111111111"
const discoveryCursor = "fixture-page-2"

func discoveryFixturePage(query url.Values) api.DebugTelemetryListResponse {
	cold := false
	var coldFilter *bool
	if query.Has("cold_boot") {
		cold, _ = strconv.ParseBool(query.Get("cold_boot"))
		coldFilter = &cold
	}
	status, _ := strconv.Atoi(query.Get("status"))
	latency, _ := strconv.Atoi(query.Get("min_latency_ms"))
	r := requestFixtureEvidence().Request
	r.Guest = withRequestGuest(requestFixtureEvidence()).Request.Guest
	r.DeploymentID, r.Route, r.ReceivedAt, r.Count, r.ColdBoot = query.Get("deployment_id"), "/orders", "2026-10-17T10:59:00Z", 7, cold
	if query.Get("route") != "" {
		r.Route = query.Get("route")
	}
	page := api.DebugTelemetryListResponse{Since: "1h", WindowStart: "2026-10-17T10:00:00Z", WindowEnd: "2026-10-17T11:00:00Z", Requests: []api.DebugTelemetryRequestItem{r}, NextCursor: discoveryCursor,
		Filters: api.DebugTelemetryListFilters{DeploymentID: query.Get("deployment_id"), Status: status, MinLatencyMS: latency, ColdBoot: coldFilter}}
	if query.Get("cursor") == discoveryCursor {
		page.Complete, page.NextCursor = true, ""
		page.Requests[0].ID, page.Requests[0].ReceivedAt = "row-2", "2026-10-17T10:58:00Z"
	}
	return page
}

func TestRequestDiscoveryPaginationAndConsent(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, approved := range []bool{false, true} {
			t.Run(version+"/approved="+strconv.FormatBool(approved), func(t *testing.T) {
				var pages, deployments atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						t.Error("discovery performed a native write")
					}
					switch r.URL.Path {
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/deployments/" + discoveryDeploymentID:
						deployments.Add(1)
						_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: discoveryDeploymentID, AppID: "app-1", Scope: "staging"})
					case "/v1/apps/alpha/debug/requests":
						pages.Add(1)
						q := r.URL.Query()
						for key, value := range map[string]string{"since": "1h", "route": "/orders", "status": "503", "deployment_id": discoveryDeploymentID, "min_latency_ms": "50", "cold_boot": "false", "limit": "1"} {
							if q.Get(key) != value {
								t.Errorf("lost %s=%s: %v", key, value, q)
							}
						}
						_ = json.NewEncoder(w).Encode(discoveryFixturePage(q))
					default:
						t.Errorf("unexpected discovery endpoint %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer backend.Close()
				consent := &EnvironmentConsent{}
				if approved {
					consent.RequestReadApps = map[string]string{"alpha": "app-1"}
				}
				cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, Environments: consent}, version)
				warm := false
				args := requestsArgs{App: "alpha", Since: "1h", Route: "/orders", DeploymentID: discoveryDeploymentID, Status: 503, MinLatencyMS: 50, ColdBoot: &warm, Limit: 1}
				page := callTool(t, ctx, cs, "gregale_list_requests", args, !approved)
				if !approved {
					if string(page["code"]) != `"request_access_denied"` || pages.Load() != 0 || deployments.Load() != 0 {
						t.Fatalf("read without diagnostic consent: %s", page)
					}
					return
				}
				if pages.Load() != 1 || deployments.Load() != 2 || string(page["complete"]) != "false" {
					t.Fatalf("unexpected page walk or deployment reads: %s", page)
				}
				var rows []requestView
				_ = json.Unmarshal(page["requests"], &rows)
				encoded, _ := json.Marshal(page)
				if len(rows) != 1 || rows[0].LookupIdentifier != "row-1" || rows[0].Scope != "staging" || rows[0].Count == nil || *rows[0].Count != 7 || strings.Contains(string(encoded), "excluded-") {
					t.Fatalf("bad projection: %s", encoded)
				}
				var next requestsArgs
				if json.Unmarshal(page["next_arguments"], &next) != nil || next.Cursor != discoveryCursor || next.ColdBoot == nil || *next.ColdBoot {
					t.Fatal("continuation lost warm filter or cursor")
				}
				last := callTool(t, ctx, cs, "gregale_list_requests", next, false)
				if pages.Load() != 2 || deployments.Load() != 4 || string(last["complete"]) != "true" || last["next_arguments"] != nil || last["next_cursor"] != nil || string(last["window_start"]) != string(page["window_start"]) {
					t.Fatalf("bad terminal page: %s", last)
				}
				denied := callTool(t, ctx, cs, "gregale_get_deployment", deploymentArgs{DeploymentID: discoveryDeploymentID}, true)
				if string(denied["code"]) != `"environment_access_denied"` {
					t.Fatalf("diagnostic filter granted ordinary deployment access: %s", denied)
				}
			})
		}
	}
}

func TestRequestDiscoveryInvalidInputsAndResponses(t *testing.T) {
	for _, args := range []requestsArgs{{Limit: -1}, {Limit: api.MCPRequestListMax + 1}, {Since: "nonsense"}, {Since: "0h"}, {Since: "999999999999d"}, {Since: " 1h"}, {Route: "/a?secret"}, {Route: "/a#secret"}, {Route: "/a\nb"}, {Route: strings.Repeat("x", 257)}, {DeploymentID: "other"}, {Status: 99}, {Status: 600}, {MinLatencyMS: -1}, {MinLatencyMS: api.MCPRequestMinLatencyMaxMS + 1}, {Cursor: " "}, {Cursor: strings.Repeat("x", api.MCPRequestCursorMaxBytes+1)}} {
		if _, err := requestListOptions(args); err == nil {
			t.Fatalf("accepted invalid discovery args: %+v", args)
		}
	}
	for _, since := range []string{"1h", "1.5h", "3d", "106751d"} {
		if _, err := requestListOptions(requestsArgs{Since: since}); err != nil {
			t.Fatalf("rejected valid lookback %s", since)
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*api.DebugTelemetryListResponse)
	}{
		{"oversized", func(p *api.DebugTelemetryListResponse) { p.Requests = append(p.Requests, p.Requests[0]) }},
		{"missing_cursor", func(p *api.DebugTelemetryListResponse) { p.NextCursor = "" }},
		{"complete_with_cursor", func(p *api.DebugTelemetryListResponse) { p.Complete = true }},
		{"stalled_cursor", func(p *api.DebugTelemetryListResponse) { p.NextCursor = "prior-page" }},
		{"oversized_cursor", func(p *api.DebugTelemetryListResponse) {
			p.NextCursor = strings.Repeat("x", api.MCPRequestCursorMaxBytes+1)
		}},
		{"credential_cursor", func(p *api.DebugTelemetryListResponse) { p.NextCursor = testToken }},
		{"wrong_filter", func(p *api.DebugTelemetryListResponse) { p.Filters.Status = 200 }},
		{"unexpected_consumer", func(p *api.DebugTelemetryListResponse) { p.Filters.ConsumerID = "excluded-consumer" }},
		{"wrong_row", func(p *api.DebugTelemetryListResponse) { p.Requests[0].Status = 200 }},
		{"wrong_window", func(p *api.DebugTelemetryListResponse) { p.Requests[0].ReceivedAt = "2026-10-17T09:00:00Z" }},
		{"missing_lookup", func(p *api.DebugTelemetryListResponse) { p.Requests[0].ID = "" }},
		{"missing_window", func(p *api.DebugTelemetryListResponse) { p.WindowStart = "" }},
		{"false_window", func(p *api.DebugTelemetryListResponse) { p.Since = "2h" }},
		{"unreported_clamp", func(p *api.DebugTelemetryListResponse) { p.Since = "30m"; p.WindowStart = "2026-10-17T10:30:00Z" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					return
				}
				p := discoveryFixturePage(r.URL.Query())
				test.mutate(&p)
				_ = json.NewEncoder(w).Encode(p)
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_list_requests", requestsArgs{App: "alpha", Since: "1h", Status: 503, Limit: 1, Cursor: "prior-page"}, true)
			if string(out["code"]) != `"invalid_response"` || out["requests"] != nil {
				t.Fatalf("inconsistent native page released: %s", out)
			}
		})
	}
}

func TestRequestDiscoveryRetentionErrorsAndIdentity(t *testing.T) {
	for _, mode := range []string{"clamped", "empty", "plan_gated", "retention_miss", "cursor_expired", "replaced_app", "wrong_deployment", "wrong_filter_deployment", "changed_scope", "deduplicated"} {
		t.Run(mode, func(t *testing.T) {
			var pages, deployments atomic.Int32
			var replaced atomic.Bool
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("discovery wrote native state")
					w.WriteHeader(405)
					return
				}
				switch r.URL.Path {
				case "/v1/apps/alpha":
					id := "app-1"
					if replaced.Load() {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
				case "/v1/deployments/" + discoveryDeploymentID:
					n := deployments.Add(1)
					id, scope := "app-1", "staging"
					if mode == "wrong_deployment" || mode == "wrong_filter_deployment" {
						id = "other-app"
					}
					if mode == "changed_scope" && n > 1 {
						scope = "production"
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: discoveryDeploymentID, AppID: id, Scope: scope})
				default:
					pages.Add(1)
					if status := map[string]int{"plan_gated": 403, "retention_miss": 404, "cursor_expired": 400}[mode]; status != 0 {
						api.WriteProblem(w, api.NewProblem(status, "native_discovery_error", "Unavailable", "native plan or retention denied"))
						return
					}
					p := discoveryFixturePage(r.URL.Query())
					p.Complete, p.NextCursor = true, ""
					if mode == "clamped" {
						p.RetentionClamped = true
						p.Since = "30m"
						p.WindowStart = "2026-10-17T10:30:00Z"
					}
					if mode == "empty" {
						p.Requests = nil
					}
					if mode == "replaced_app" {
						replaced.Store(true)
					}
					if mode == "wrong_deployment" || mode == "changed_scope" || mode == "deduplicated" {
						p.Requests[0].DeploymentID = discoveryDeploymentID
					}
					if mode == "deduplicated" {
						p.Requests = append(p.Requests, p.Requests[0])
						p.Requests[1].ID = "row-2"
					}
					_ = json.NewEncoder(w).Encode(p)
				}
			}))
			defer b.Close()
			cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL, Environments: &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}}, "2026-07-28")
			failed := mode != "clamped" && mode != "empty" && mode != "deduplicated"
			args := requestsArgs{App: "alpha", Since: "1h"}
			wantPages := int32(1)
			if mode == "wrong_filter_deployment" {
				args.DeploymentID = discoveryDeploymentID
				wantPages = 0
			}
			out := callTool(t, ctx, cs, "gregale_list_requests", args, failed)
			if pages.Load() != wantPages {
				t.Fatal("discovery retried or walked pages")
			}
			if failed && (out["requests"] != nil || out["next_cursor"] != nil) {
				t.Fatal("released evidence on error")
			}
			if strings.HasSuffix(mode, "gated") || mode == "retention_miss" || mode == "cursor_expired" {
				if string(out["code"]) != `"native_discovery_error"` {
					t.Fatal("native error replaced")
				}
			}
			if mode == "clamped" && (string(out["retention_clamped"]) != "true" || string(out["since"]) != `"30m"`) {
				t.Fatalf("lost clamp: %s", out)
			}
			if mode == "empty" && (string(out["requests"]) != "[]" || string(out["complete"]) != "true") {
				t.Fatalf("invented empty evidence: %s", out)
			}
			if mode == "deduplicated" && deployments.Load() != 2 {
				t.Fatalf("deployment reads not bounded by distinct IDs: %d", deployments.Load())
			}
		})
	}
}

func TestRequestDiscoveryPrompt(t *testing.T) {
	var pages atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/alpha" {
			pages.Add(1)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
	}))
	defer b.Close()
	cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL, Environments: &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}}, "2026-07-28")
	args := map[string]string{"app": "alpha", "since": "1h", "status": "503", "min_latency_ms": "50", "cold_boot": "false", "limit": "1"}
	prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_request_diagnosis", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	text := prompt.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(text, "gregale_list_requests") || !strings.Contains(text, `"cold_boot":false`) || pages.Load() != 0 {
		t.Fatalf("prompt did not preserve discovery intent: %s", text)
	}
	for _, bad := range []map[string]string{{"app": "alpha", "request_id": "row-1", "since": "1h"}, {"app": "alpha", "since": "bogus"}, {"app": "alpha", "status": "0"}, {"app": "alpha", "cold_boot": "sometimes"}, {"app": "alpha", "limit": "0"}, {"app": "alpha", "consumer_id": "excluded"}} {
		if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_request_diagnosis", Arguments: bad}); err == nil {
			t.Fatalf("invalid diagnosis args accepted: %v", bad)
		}
	}
	p := completionParams("since", "1", map[string]string{"app": "alpha", "cold_boot": "false", "min_latency_ms": "50"})
	p.Ref.Name = "gregale_request_diagnosis"
	res, err := cs.Complete(ctx, p)
	if err != nil || len(res.Completion.Values) != 0 || pages.Load() != 0 {
		t.Fatalf("completion enumerated private data: %+v %v", res, err)
	}
}
