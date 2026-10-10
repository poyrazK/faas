package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

// adr: 967 — the simulator renders templated redirects and headers with
// the simulated request, as the gateway would.
func TestSimulateExpandsTemplates(t *testing.T) {
	rules := []api.EdgeRuleResponse{
		{ID: "hdr", AppID: "app", Enabled: true, Kind: "headers", MatchHost: "*", MatchPath: "/*", Priority: 5,
			Action: json.RawMessage(`{"headers":{"request_headers":[{"name":"X-Client-Country","action":"set","value":"${country}/AS${asn}","template":true}]}}`)},
		{ID: "redir", AppID: "app", Enabled: true, Kind: "redirect", MatchHost: "*", MatchPath: "/old/*", Priority: 10,
			Action: json.RawMessage(`{"redirect":{"status_code":308,"to":"https://new.example${path}","template":true}}`)},
	}
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/old/a b", Method: http.MethodGet,
		AppMaintenanceLoaded: true, Country: "DE", ASN: 13335}
	result, err := edgeruletrace.Simulate(input, rules)
	if err != nil {
		t.Fatal(err)
	}
	if result.Simulation.Location != "https://new.example/old/a%20b" {
		t.Fatalf("location = %q", result.Simulation.Location)
	}
	// The redirect ends the request before the headers phase, as on the
	// gateway; trace a path it does not match for the headers rule.
	input.Path = "/other"
	if result, err = edgeruletrace.Simulate(input, rules); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, step := range result.Simulation.Steps {
		for _, op := range step.RequestOps {
			if op.Name == "X-Client-Country" {
				found = true
				if op.Value != "DE/AS13335" {
					t.Fatalf("header value = %q", op.Value)
				}
			}
		}
	}
	if !found {
		t.Fatal("templated request header op not in the trace")
	}
}
