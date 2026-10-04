package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRouteChangesCLI(t *testing.T) {
	for _, scenario := range []string{"regression", "intent changed", "unknown", "unavailable", "foreign changes", "missing evidence"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			result := automaticResultFixture()
			result.CheckID = uuid.NewString()
			result.Changes = &api.RouteCheckChanges{Version: 1, CheckID: result.CheckID, Status: "comparable", Summary: api.RouteCheckChangeSummary{NewlyViolated: 1}, Truncated: true, Findings: []api.RouteFindingChange{{Method: "POST", Path: "/billing/{id}", Requirement: "budget", Kind: "newly_violated", Before: &api.RouteRequirementsFinding{Actual: "budget_ms=500"}, After: &api.RouteRequirementsFinding{Actual: "budget_ms=2000\x1b[31m"}}}}
			want, contains := 0, "New violations: 1"
			switch scenario {
			case "intent changed":
				result.Changes.Status, contains = "requirements_changed", "requirements_changed"
			case "unknown":
				result.Changes.Status, contains = "unavailable", "unavailable"
			case "unavailable":
				result.Changes, contains = nil, "refresh to retain"
			case "foreign changes":
				result.Changes.CheckID, want, contains = uuid.NewString(), 1, "not bound"
			case "missing evidence":
				result.State, result.Freshness, result.Check, result.CheckedAt = "pending", "unavailable", nil, nil
				want, contains = 1, "not bound"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSONTest(w, result) }))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			old, oldErr := osStdout, osStderr
			osStdout, osStderr = &out, &out
			t.Cleanup(func() { osStdout, osStderr = old, oldErr })
			code := run([]string{"routes", "results", "my-api", "--deployment", result.DeploymentID, "--changes"})
			if code != want || !strings.Contains(out.String(), contains) || strings.Contains(out.String(), "\x1b") {
				t.Fatalf("changes output: exit=%d want=%d; %q", code, want, out.String())
			}
			if scenario == "regression" && (!strings.Contains(out.String(), "/billing/{id}") || !strings.Contains(out.String(), "500") || !strings.Contains(out.String(), "2000") || !strings.Contains(out.String(), "truncated")) {
				t.Fatal("change detail or truncation omitted")
			}
		})
	}
}
