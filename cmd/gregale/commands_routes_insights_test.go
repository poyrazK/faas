package main_test

// adr: 1198

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func routeInsightsServer(t *testing.T, insights []api.RouteInsight) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/routes/insights" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		main.WriteJSONTest(w, api.RouteInsightsResponse{Slug: "demo", Insights: insights})
	}))
}

func runRouteInsightsCLI(t *testing.T, server *httptest.Server, args ...string) (int, string) {
	t.Helper()
	main.ResetJSONOut(t)
	main.SetPreviewTestAuth(t)
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old := (*main.OsStdout)
	(*main.OsStdout) = &output
	t.Cleanup(func() { (*main.OsStdout) = old })
	return main.Run(args), output.String()
}

func TestRoutesInsightsCLI(t *testing.T) {
	server := routeInsightsServer(t, []api.RouteInsight{
		{Kind: api.RouteInsightKeptAwake, Method: "GET", Route: "/health", State: api.RouteInsightActive,
			Message: "GET /health is called about every 1m0s and keeps demo awake.", Evidence: json.RawMessage(`{"awake_share":0.96}`),
			FirstDetectedAt: "2026-10-15T06:00:00Z", LastDetectedAt: "2026-10-15T12:00:00Z"},
		{Kind: api.RouteInsightCacheable, Method: "GET", Route: "/catalog", State: api.RouteInsightResolved,
			Message: "GET /catalog returned the same body.", Evidence: json.RawMessage(`{}`),
			FirstDetectedAt: "2026-10-12T06:00:00Z", LastDetectedAt: "2026-10-13T06:00:00Z", ResolvedAt: "2026-10-14T06:00:00Z"},
	})
	defer server.Close()
	code, out := runRouteInsightsCLI(t, server, "routes", "insights", "demo")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"Route insights for demo",
		"[kept_awake] GET /health — active since 2026-10-15T06:00:00Z",
		"keeps demo awake",
		"[cacheable] GET /catalog — resolved 2026-10-14T06:00:00Z",
		"routes.insight.detected",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	code, out = runRouteInsightsCLI(t, server, "routes", "insights", "demo", "--json")
	var parsed api.RouteInsightsResponse
	if code != 0 || json.Unmarshal([]byte(out), &parsed) != nil || len(parsed.Insights) != 2 {
		t.Fatalf("json: exit %d %s", code, out)
	}
}

func TestRoutesInsightsCLIEmptyAndInvalid(t *testing.T) {
	server := routeInsightsServer(t, nil)
	defer server.Close()
	if code, out := runRouteInsightsCLI(t, server, "routes", "insights", "demo"); code != 0 || !strings.Contains(out, "No route insights for demo") {
		t.Fatalf("empty: exit %d %s", code, out)
	}
	for _, args := range [][]string{{"routes", "insights"}, {"routes", "insights", "Not_A_Slug"}} {
		if code, _ := runRouteInsightsCLI(t, server, args...); code == 0 {
			t.Fatalf("%v: want a non-zero exit", args)
		}
	}
}

// adr: 20261020161310 — --fix lists the insights, then applies the fix the
// listing showed by sending its fix id back.
func TestRoutesInsightsFixCLI(t *testing.T) {
	insights := []api.RouteInsight{
		{ID: "ins-1", Kind: api.RouteInsightCacheStale, Method: "GET", Route: "/catalog", State: api.RouteInsightActive,
			Message: "GET /catalog is cached for 5m0s, but refills found it changed.", Evidence: json.RawMessage(`{}`),
			FirstDetectedAt: "2026-10-20T06:00:00Z", LastDetectedAt: "2026-10-20T12:00:00Z",
			Fix: &api.RouteInsightFix{ID: "fix-abc", Action: api.RouteInsightFixSetCacheMaxAge, Summary: "Change the max-age of cache rule r1 from 300s to 30s. Then purge the route's cached responses.", MaxAgeSeconds: 30, RuleID: "r1", PurgeCache: true}},
		{ID: "ins-2", Kind: api.RouteInsightKeptAwake, Method: "GET", Route: "/health", State: api.RouteInsightActive,
			Message: "GET /health keeps demo awake.", Evidence: json.RawMessage(`{}`), FirstDetectedAt: "2026-10-20T06:00:00Z", LastDetectedAt: "2026-10-20T12:00:00Z"},
	}
	var fixed []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo/routes/insights":
			main.WriteJSONTest(w, api.RouteInsightsResponse{Slug: "demo", Insights: insights})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/routes/insights/ins-1/fix":
			var req api.RouteInsightFixRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode: %v", err)
			}
			fixed = append(fixed, req.FixID)
			main.WriteJSONTest(w, api.RouteInsightFixResponse{Fix: *insights[0].Fix, Rule: api.EdgeRuleResponse{ID: "r1"}, CachePurge: api.RouteInsightPurgeRequested, Resolved: 1})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	code, out := runRouteInsightsCLI(t, server, "routes", "insights", "demo")
	if code != 0 || !strings.Contains(out, "Fix:  Change the max-age of cache rule r1") || !strings.Contains(out, "ID:   ins-1") ||
		!strings.Contains(out, "gregale routes insights demo --fix ID") || strings.Contains(out, "ID:   ins-2") {
		t.Fatalf("listing: exit %d\n%s", code, out)
	}
	code, out = runRouteInsightsCLI(t, server, "routes", "insights", "demo", "--fix", "ins-1")
	if code != 0 || len(fixed) != 1 || fixed[0] != "fix-abc" {
		t.Fatalf("fix: exit %d fixed %v\n%s", code, fixed, out)
	}
	for _, want := range []string{"Applied: Change the max-age of cache rule r1", "Edge rule: r1", "Cache purge requested for GET /catalog."} {
		if !strings.Contains(out, want) {
			t.Fatalf("fix output missing %q:\n%s", want, out)
		}
	}
	for _, id := range []string{"ins-2", "missing"} {
		if code, _ := runRouteInsightsCLI(t, server, "routes", "insights", "demo", "--fix", id); code == 0 {
			t.Fatalf("--fix %s: want a non-zero exit", id)
		}
	}
	if len(fixed) != 1 {
		t.Fatalf("refused fixes were sent: %v", fixed)
	}
}
