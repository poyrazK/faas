package main_test

// adr: 1152

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type visibilityAPI struct {
	writes []string
	bodies []string
}

func (v *visibilityAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	on := true
	advice := api.RouteAdviceResponse{Slug: "demo", Suggestions: []api.RouteAdviceSuggestion{
		{ID: "wake", Kind: api.RouteAdviceKindWakeAhead, Title: "Turn on service wake-ahead for demo", Rules: []api.CreateEdgeRuleRequest{},
			Setting: &api.RouteAdviceSetting{WakeAhead: &on}},
		{ID: "prio", Kind: api.RouteAdviceKindPriority, Title: "Set route priorities for demo", Rules: []api.CreateEdgeRuleRequest{},
			Setting: &api.RouteAdviceSetting{RoutePriorities: []api.RoutePriorityRule{{Method: "POST", Path: "/checkout", Class: "critical"}}}},
	}}
	learned := api.ServiceWakeAheadLearned{Source: api.RouteEvidenceSourceLive, Window: "24h", CallerWakes: 40, Targets: []api.ServiceWakeAheadTarget{
		{Slug: "auth", CallShare: 0.9, ColdShare: 0.75, Qualifies: true, Started: 12, Used: 10, Unused: 2},
	}}
	queue := api.RoutePriorityQueueStats{Source: api.RouteEvidenceSourceLive, Window: "24h", Classes: []api.RoutePriorityQueueClass{
		{Class: "critical", Queued: 30, Displacing: 4}, {Class: "normal", Queued: 50, Displaced: 4, Rejected: 7}, {Class: "bulk"},
	}}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			v.writes = append(v.writes, r.Method+" "+r.URL.Path)
			v.bodies = append(v.bodies, string(body))
		}
		switch r.URL.Path {
		case "/v1/apps/demo/routes/advice":
			main.WriteJSONTest(w, advice)
		case "/v1/apps/demo/service-wake-ahead":
			main.WriteJSONTest(w, api.ServiceWakeAheadResponse{Slug: "demo", Enabled: r.Method == http.MethodPut, Learned: &learned})
		case "/v1/apps/demo/route-priorities":
			main.WriteJSONTest(w, api.RoutePrioritiesResponse{Slug: "demo", Source: api.RoutePrioritySourceConfigured,
				Routes: []api.RoutePriorityRule{{Method: "POST", Path: "/checkout", Class: "critical"}}, Queue: &queue})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func runVisibilityCLI(t *testing.T, server *httptest.Server, args ...string) (int, string) {
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

func TestRoutesAdviseCLIAppliesSettingSuggestions(t *testing.T) {
	v := &visibilityAPI{}
	server := v.server(t)
	defer server.Close()
	code, out := runVisibilityCLI(t, server, "routes", "advise", "demo")
	if code != 0 || !strings.Contains(out, "Setting: service wake-ahead on") || !strings.Contains(out, "Setting: critical POST /checkout") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if code, out := runVisibilityCLI(t, server, "routes", "advise", "demo", "--apply", "wake"); code != 0 || !strings.Contains(out, "Service wake-ahead is on for demo") {
		t.Fatalf("apply wake: exit %d:\n%s", code, out)
	}
	if code, out := runVisibilityCLI(t, server, "routes", "advise", "demo", "--apply", "prio"); code != 0 || !strings.Contains(out, "Route priorities for demo (saved)") {
		t.Fatalf("apply priorities: exit %d:\n%s", code, out)
	}
	if len(v.writes) != 2 || v.writes[0] != "PUT /v1/apps/demo/service-wake-ahead" || v.writes[1] != "PUT /v1/apps/demo/route-priorities" {
		t.Fatalf("writes = %v", v.writes)
	}
	var rules api.SetRoutePrioritiesRequest
	if err := json.Unmarshal([]byte(v.bodies[1]), &rules); err != nil || len(rules.Routes) != 1 || rules.Routes[0].Path != "/checkout" {
		t.Fatalf("priorities body = %s (%v)", v.bodies[1], err)
	}
}

func TestWakeAheadAndPriorityCLIShowEvidence(t *testing.T) {
	v := &visibilityAPI{}
	server := v.server(t)
	defer server.Close()
	code, out := runVisibilityCLI(t, server, "wake-ahead", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"Measured services over 40 cold wakes", "auth", "90%", "75%", "yes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("wake-ahead output missing %q:\n%s", want, out)
		}
	}
	code, out = runVisibilityCLI(t, server, "routes", "priority", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"Queue outcomes (last 24h)", "TURNED AWAY", "normal"} {
		if !strings.Contains(out, want) {
			t.Fatalf("priority output missing %q:\n%s", want, out)
		}
	}
	if len(v.writes) != 0 {
		t.Fatalf("reads must not write: %v", v.writes)
	}
}
