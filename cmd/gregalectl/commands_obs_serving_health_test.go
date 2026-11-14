package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func servingCLIResponse() api.ObsServingHealth {
	now := time.Now().UTC().Truncate(time.Second)
	on, expected, reporting := true, 1, true
	run := now.Add(-time.Minute)
	probe := api.ObsServingProbe{Node: "fsn-2", URLID: strings.Repeat("a", 64), State: "healthy", Reporting: &reporting, EvaluatedAt: &now, LastRunAt: &run, ExpectedFirstByteSeconds: 2, MaxResultAgeSeconds: 900}
	counts := operatorobs.ServingProbeCounts([]api.ObsServingProbe{probe})
	return api.ObsServingHealth{GeneratedAt: now, Scope: operatorobs.ServingHealthScope, State: "healthy", MaxAgeSeconds: 120, Inventory: api.ObsServingProbeInventory{State: "healthy", Configured: &on, ExpectedPairs: &expected, ObservedAt: &now}, Counts: &counts, Probes: []api.ObsServingProbe{probe}, Limitations: operatorobs.ServingHealthLimitations()}
}

func TestCmdObsServingHealth(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	response := servingCLIResponse()
	mode := "ok"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != operatorobs.ServingHealthPath || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("serving request changed the protected read contract")
		}
		if mode == "error" {
			http.Error(w, "private provider response", 503)
			return
		}
		if mode == "redirect" {
			http.Redirect(w, r, "http://private.invalid", 307)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	if code := cmdObsDispatch([]string{subObsServingHealth}); code != 0 || !strings.Contains(stdout.String(), "serving_health=healthy") || !strings.Contains(stdout.String(), "node=fsn-2 url_id=") || !strings.Contains(stdout.String(), "reporting=1 healthy=1") || !strings.Contains(stdout.String(), "url_identity=sha256_exact_url") {
		t.Fatalf("serving human: %d %s %s", code, stdout, stderr)
	}
	stdout.Reset()
	probe := &response.Probes[0]
	probe.State, probe.Reason, probe.Reporting, probe.LastRunAt = "unknown", "coverage_missing", nil, nil
	counts := operatorobs.ServingProbeCounts(response.Probes)
	response.Counts = &counts
	response.State, response.Reason = operatorobs.ServingProbeOverall(counts)
	if code := cmdObsServingHealth([]string{"--json"}); code != 0 || !strings.Contains(stdout.String(), `"reporting": null`) || !strings.Contains(stdout.String(), `"unknown": 1`) {
		t.Fatalf("serving JSON unknown: %d %s %s", code, stdout, stderr)
	}
	before := calls
	for _, args := range [][]string{{"--url", "https://private.example/token"}, {"--timeout", "0s"}, {"extra"}, {"--since", "now"}} {
		if code := cmdObsServingHealth(args); code != 2 {
			t.Fatal("invalid serving arguments accepted")
		}
	}
	if calls != before {
		t.Fatal("invalid arguments reached the API")
	}
	for _, failure := range []string{"error", "redirect"} {
		mode = failure
		stdout.Reset()
		stderr.Reset()
		if code := cmdObsServingHealth(nil); code != 1 || stdout.Len() != 0 || strings.Contains(stderr.String(), "private") {
			t.Fatalf("unsafe serving failure: %d %s %s", code, stdout, stderr)
		}
	}
	mode = "ok"
	response.Probes[0].URLID = "https://private.example/token"
	for _, args := range [][]string{nil, {"--json"}} {
		stdout.Reset()
		stderr.Reset()
		if code := cmdObsServingHealth(args); code != 1 || stdout.Len() != 0 || strings.Contains(stderr.String(), "private") {
			t.Fatal("unsafe URL metadata was emitted")
		}
	}
}

func TestObsServingHealthContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*api.ObsServingHealth)
	}{
		{"unknown scope", func(out *api.ObsServingHealth) { out.Scope = "private-scope" }},
		{"raw URL identity", func(out *api.ObsServingHealth) { out.Probes[0].URLID = "https://private.example/" }},
		{"unsafe node", func(out *api.ObsServingHealth) { out.Probes[0].Node = "private\nnode" }},
		{"unknown state", func(out *api.ObsServingHealth) { out.Probes[0].State = "private-state" }},
		{"provider reason", func(out *api.ObsServingHealth) { out.Probes[0].Reason = "private-provider-response" }},
		{"nil probes", func(out *api.ObsServingHealth) { out.Probes = nil }},
		{"duplicate pair", func(out *api.ObsServingHealth) { out.Probes = append(out.Probes, out.Probes[0]) }},
		{"count mismatch", func(out *api.ObsServingHealth) { out.Counts.Healthy = 0 }},
		{"unknown expected", func(out *api.ObsServingHealth) { out.Inventory.ExpectedPairs = nil }},
		{"old inventory", func(out *api.ObsServingHealth) {
			at := out.GeneratedAt.Add(-3 * time.Minute)
			out.Inventory.ObservedAt = &at
		}},
		{"old healthy evaluation", func(out *api.ObsServingHealth) {
			at := out.GeneratedAt.Add(-3 * time.Minute)
			out.Probes[0].EvaluatedAt = &at
		}},
		{"expired healthy run", func(out *api.ObsServingHealth) { at := out.GeneratedAt.Add(-time.Hour); out.Probes[0].LastRunAt = &at }},
		{"future run", func(out *api.ObsServingHealth) { at := out.GeneratedAt.Add(time.Minute); out.Probes[0].LastRunAt = &at }},
		{"healthy without reporting", func(out *api.ObsServingHealth) { out.Probes[0].Reporting = nil }},
		{"nonfinite policy", func(out *api.ObsServingHealth) { out.Probes[0].MaxResultAgeSeconds = math.Inf(1) }},
		{"missing limitations", func(out *api.ObsServingHealth) { out.Limitations = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := servingCLIResponse()
			tc.mutate(&out)
			if validObsServingHealth(out) {
				t.Fatal("invalid serving contract accepted")
			}
		})
	}
	unknown := operatorobs.ServingHealth(t.Context(), nil, time.Now().UTC())
	if !validObsServingHealth(unknown) {
		t.Fatal("unknown serving source was rejected")
	}
}
