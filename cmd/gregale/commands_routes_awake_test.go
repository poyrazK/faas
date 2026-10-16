package main

// adr: 1187, 1199

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func routeAwakeServer(t *testing.T, queries *[]string) *httptest.Server {
	t.Helper()
	report := api.RouteAwakeResponse{
		Slug: "demo", IdleTimeoutSeconds: 300, WindowSeconds: 7 * 24 * 3600, AwakeSeconds: 120 * 3600, AwakeShare: 0.71,
		GBHours: 31.2, Wakes: 14, UnattributedSeconds: 3600, OtherRoutesSeconds: 1800, RoutesLimit: 10, MinInstances: 1,
		Sources: []api.RouteAwakeSource{
			{Source: api.RouteAwakeSourceRouteProbe, Events: 600, AwakeSeconds: 2 * 3600, AwakeShare: 0.02},
			{Source: api.RouteAwakeSourcePrewarm, Events: 3},
		},
		Routes: []api.RouteAwakeRoute{{
			Method: "GET", Route: "/health", Requests: 10080, Wakes: 2, AwakeSeconds: 110 * 3600, AwakeShare: 0.92,
			Pattern: api.RouteAwakePatternPeriodic, IntervalSeconds: 60,
			IfRemoved:  api.RouteAwakeWhatIf{AwakeSecondsSaved: 100 * 3600, WakesChange: 40},
			Suggestion: &api.RouteAwakeSuggestion{Kind: api.RouteAwakeSuggestEdgeCache, Message: "GET /health is called about every 1m0s"},
		}, {
			Method: "POST", Route: "/orders", Requests: 300, Wakes: 12, AwakeSeconds: 5 * 3600, AwakeShare: 0.04,
			Pattern: api.RouteAwakePatternIrregular,
		}},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/routes/awake" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		*queries = append(*queries, r.URL.RawQuery)
		writeJSONTest(w, report)
	}))
}

func runRouteAwakeCLI(t *testing.T, server *httptest.Server, args ...string) (int, string) {
	t.Helper()
	resetJSONOut(t)
	setPreviewTestAuth(t)
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	return run(args), output.String()
}

func TestRoutesAwakeCLI(t *testing.T) {
	var queries []string
	server := routeAwakeServer(t, &queries)
	defer server.Close()
	code, out := runRouteAwakeCLI(t, server, "routes", "awake", "demo", "--since", "24h")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"demo was awake 71% of the last 168h 0m: 120h 0m running, 31.2 GB-h, 14 wakes. Idle timeout: 300s.",
		"GET /health", "every ~60s", "-100h 0m awake, +40 wakes", "POST /orders", "no change",
		"Other routes", "Unattributed", "Suggestions", "GET /health is called about every 1m0s",
		"Route probes (canary)", "keeps at least 1 instance(s) running",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Scheduled prewarm") {
		t.Fatalf("a source that explains no awake time is not listed:\n%s", out)
	}
	if len(queries) != 1 || queries[0] != "since=24h" {
		t.Fatalf("queries = %v", queries)
	}
}

func TestRoutesAwakeCLIJSON(t *testing.T) {
	var queries []string
	server := routeAwakeServer(t, &queries)
	defer server.Close()
	code, out := runRouteAwakeCLI(t, server, "routes", "awake", "demo", "--json")
	var report api.RouteAwakeResponse
	if code != 0 || json.Unmarshal([]byte(out), &report) != nil || report.Slug != "demo" || len(report.Routes) != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
}

func TestRoutesAwakeCLIRejectsBadArguments(t *testing.T) {
	var queries []string
	server := routeAwakeServer(t, &queries)
	defer server.Close()
	for _, args := range [][]string{
		{"routes", "awake"},
		{"routes", "awake", "Not_A_Slug"},
		{"routes", "awake", "demo", "--since", "yesterday"},
	} {
		if code, _ := runRouteAwakeCLI(t, server, args...); code == 0 {
			t.Fatalf("%v: want a non-zero exit", args)
		}
	}
	if len(queries) != 0 {
		t.Fatalf("invalid arguments must not reach the API: %v", queries)
	}
}

func TestAwakeDuration(t *testing.T) {
	for seconds, want := range map[int64]string{0: "0s", 45: "45s", 60: "1m", 3599: "59m", 3600: "1h 0m", 90061: "25h 1m"} {
		if got := awakeDuration(seconds); got != want {
			t.Fatalf("awakeDuration(%d) = %q, want %q", seconds, got, want)
		}
	}
}
