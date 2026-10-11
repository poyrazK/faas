package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type routeAdviceTestAPI struct {
	mu      sync.Mutex
	query   string
	created []api.CreateEdgeRuleRequest
	failAt  int
}

func (a *routeAdviceTestAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	disabled := false
	advice := api.RouteAdviceResponse{
		Slug: "demo", From: "2026-10-03T00:00:00Z", Until: "2026-10-10T00:00:00Z", CacheMaxAgeSeconds: 60, RoutesAnalyzed: 3,
		Suggestions: []api.RouteAdviceSuggestion{{
			ID: "abc123def456", Kind: "cache", Method: "GET", Route: "/catalog", Title: "Cache GET /catalog",
			Rationale: "98% of requests are anonymous and succeed.", Impact: api.RouteAdviceImpact{Summary: "about 700 of 1000 requests would be served from the edge cache"},
			Cautions: []string{"Responses can be up to 60s old."},
			Rules: []api.CreateEdgeRuleRequest{
				{MatchHost: "demo.gregale.dev", MatchPath: "/catalog", MatchMethods: []string{"GET", "HEAD"}, Enabled: &disabled, Kind: "cache", Action: json.RawMessage(`{"max_age_seconds":60}`)},
				{MatchHost: "api.example.com", MatchPath: "/catalog", MatchMethods: []string{"GET", "HEAD"}, Enabled: &disabled, Kind: "cache", Action: json.RawMessage(`{"max_age_seconds":60}`)},
			},
		}},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo/routes/advice":
			a.query = r.URL.RawQuery
			writeJSONTest(w, advice)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/edge-rules":
			var req api.CreateEdgeRuleRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode rule: %v", err)
			}
			a.created = append(a.created, req)
			if a.failAt > 0 && len(a.created) == a.failAt {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusPaymentRequired)
				_ = json.NewEncoder(w).Encode(api.Problem{Status: 402, Code: "plan_edge_rule_kind_quota_reached", Title: "Quota", Detail: "cache rule quota reached"})
				return
			}
			w.WriteHeader(http.StatusCreated)
			writeJSONTest(w, api.EdgeRuleResponse{ID: "rule-" + req.MatchHost, Kind: req.Kind, MatchHost: req.MatchHost})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func runRouteAdviseCLI(t *testing.T, server *httptest.Server, args ...string) (int, string) {
	t.Helper()
	resetJSONOut(t)
	setPreviewTestAuth(t)
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	code := run(append([]string{"routes", "advise"}, args...))
	return code, output.String()
}

func TestRoutesAdviseCLIListsSuggestions(t *testing.T) {
	fake := &routeAdviceTestAPI{}
	server := fake.server(t)
	defer server.Close()
	code, out := runRouteAdviseCLI(t, server, "demo", "--since", "3d", "--cache-max-age", "300")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"[abc123def456] cache — Cache GET /catalog", "Impact:  about 700", "Caution: Responses can be up to 60s old.", "Rules:   2 (demo.gregale.dev, api.example.com)", "--apply ID"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if fake.query != "cache_max_age=300&since=3d" || len(fake.created) != 0 {
		t.Fatalf("query = %q created = %d; listing must not write", fake.query, len(fake.created))
	}
}

func TestRoutesAdviseCLIAppliesDisabledRules(t *testing.T) {
	fake := &routeAdviceTestAPI{}
	server := fake.server(t)
	defer server.Close()
	code, out := runRouteAdviseCLI(t, server, "demo", "--apply", "abc123def456")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if len(fake.created) != 2 || fake.created[0].MatchHost != "demo.gregale.dev" || fake.created[1].MatchHost != "api.example.com" {
		t.Fatalf("created = %+v", fake.created)
	}
	for _, r := range fake.created {
		if r.Enabled == nil || *r.Enabled {
			t.Fatalf("rules must be created disabled: %+v", r)
		}
	}
	if !strings.Contains(out, "Created 2 cache rules for GET /catalog (disabled;") {
		t.Fatalf("output = %s", out)
	}
}

func TestRoutesAdviseCLIEnableAndFailures(t *testing.T) {
	fake := &routeAdviceTestAPI{}
	server := fake.server(t)
	defer server.Close()
	if code, out := runRouteAdviseCLI(t, server, "demo", "--apply", "abc123def456", "--enable"); code != 0 || len(fake.created) != 2 || !*fake.created[0].Enabled {
		t.Fatalf("enable: exit %d created %+v: %s", code, fake.created, out)
	}
	if code, _ := runRouteAdviseCLI(t, server, "demo", "--apply", "missing"); code == 0 {
		t.Fatal("unknown suggestion must fail")
	}
	fake.created, fake.failAt = nil, 2
	if code, _ := runRouteAdviseCLI(t, server, "demo", "--apply", "abc123def456"); code == 0 || len(fake.created) != 2 {
		t.Fatalf("second rule failure must fail after one create, created %d", len(fake.created))
	}
	for _, args := range [][]string{{"demo", "--enable"}, {"demo", "--since", "soon"}, {"demo", "--cache-max-age", "4000"}, {}} {
		if code, _ := runRouteAdviseCLI(t, server, args...); code == 0 {
			t.Fatalf("args %v must be rejected", args)
		}
	}
}
