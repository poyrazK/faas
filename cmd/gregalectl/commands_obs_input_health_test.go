package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func inputCLIResponse(t *testing.T, role string) api.ObsInputHealth {
	t.Helper()
	out := operatorobs.InputHealth(t.Context(), nil, role, time.Now().UTC())
	if role == "" {
		return out
	}
	one, zero := 1, 0
	for i := range out.Jobs {
		j := &out.Jobs[i]
		if j.Expectation.Source == "compute_discovery" {
			at := out.GeneratedAt
			j.Expectation = api.ObsInputExpectation{Source: "compute_discovery", State: "healthy", MinimumTargets: &one, InvalidTargets: &zero, ObservedAt: &at}
		}
		j.Targets = api.ObsInputTargets{State: "healthy", Counts: &api.ObsInputTargetCounts{Observed: 1, FreshUp: 1, MissingMinimum: &zero}}
		if j.Expectation.Source == "compute_discovery" {
			j.Targets.Counts.Extra = &zero
		}
		j.Metrics[0].State, j.Metrics[0].Reason, j.Metrics[0].Counts = "healthy", "", &api.ObsInputMetricCounts{Expected: 1, Fresh: 1}
		j.State = "healthy"
	}
	out.State = "healthy"
	return out
}

func TestCmdObsInputHealth(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	response := inputCLIResponse(t, "")
	role, mode, calls := "", "ok", 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != operatorobs.InputHealthPath || r.URL.Query().Get("expected_role") != role || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		if mode == "error" {
			http.Error(w, "private failure", 503)
			return
		}
		if mode == "redirect" {
			http.Redirect(w, r, "http://unused.invalid", http.StatusTemporaryRedirect)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	if code := cmdObsDispatch([]string{subObsInputHealth}); code != 0 || !strings.Contains(out.String(), "input_health=unknown") || !strings.Contains(out.String(), "available_role=control-plane-v1") {
		t.Fatalf("unselected: %d %s %s", code, out, stderr)
	}
	role = "control-plane-v1"
	response = inputCLIResponse(t, role)
	out.Reset()
	if code := cmdObsInputHealth([]string{"--expected-role", role}); code != 0 || !strings.Contains(out.String(), "input_health=healthy") || !strings.Contains(out.String(), "fresh_up=1") || !strings.Contains(out.String(), "readiness_value=unassessed") {
		t.Fatalf("text: %d %s %s", code, out, stderr)
	}
	out.Reset()
	if code := cmdObsInputHealth([]string{"--expected-role", role, "--json"}); code != 0 || !strings.Contains(out.String(), `"expected_role": "control-plane-v1"`) {
		t.Fatalf("JSON: %d %s %s", code, out, stderr)
	}
	before := calls
	for _, args := range [][]string{{"--expected-role", ""}, {"--expected-role", "unknown"}, {"--expected-role", role, "--expected-role", role}, {"--timeout", "0s"}, {"extra"}, {"--url", "private"}} {
		if code := cmdObsInputHealth(args); code != 2 {
			t.Fatalf("flags: %v %d", args, code)
		}
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	if code := cmdObsInputHealth(nil); code != 2 || calls != before {
		t.Fatal("invalid flags made request")
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	for _, bad := range []string{"error", "redirect", "invalid"} {
		mode = bad
		if bad == "invalid" {
			response.Jobs[0].Metrics[0].Reason = "private"
		}
		out.Reset()
		stderr.Reset()
		if code := cmdObsInputHealth([]string{"--expected-role", role, "--json"}); code != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "private") {
			t.Fatalf("invalid evidence leaked: %d %s %s", code, out, stderr)
		}
	}
}

func TestCmdObsInputHealthRejectsInconsistentEvidence(t *testing.T) {
	role := "control-plane-v1"
	base := inputCLIResponse(t, role)
	if !validObsInputHealth(base, role) {
		t.Fatal("valid fixture rejected")
	}
	for _, tc := range []struct {
		name   string
		change func(*api.ObsInputHealth)
	}{
		{"scope", func(r *api.ObsInputHealth) { r.Scope = "private" }},
		{"role", func(r *api.ObsInputHealth) { r.ExpectedRole = "single-box-v1" }},
		{"limit", func(r *api.ObsInputHealth) { r.MaxAgeSeconds++ }},
		{"missing job", func(r *api.ObsInputHealth) { r.Jobs = r.Jobs[:5] }},
		{"unsafe job", func(r *api.ObsInputHealth) { r.Jobs[0].Job = "private" }},
		{"wrong gauge", func(r *api.ObsInputHealth) { r.Jobs[0].Metrics[0].Metric = "vmmd_daemon_ready" }},
		{"missing metric", func(r *api.ObsInputHealth) { r.Jobs[0].Metrics = nil }},
		{"false healthy", func(r *api.ObsInputHealth) {
			r.Jobs[0].Metrics[0].Counts.Missing = 1
			r.Jobs[0].Metrics[0].Counts.Fresh = 0
		}},
		{"unknown became zero", func(r *api.ObsInputHealth) {
			r.Jobs[5].Expectation.State = "unknown"
			r.Jobs[5].Expectation.Reason = "discovery_missing"
		}},
		{"future discovery", func(r *api.ObsInputHealth) {
			at := r.GeneratedAt.Add(time.Hour)
			r.Jobs[5].Expectation.ObservedAt = &at
		}},
		{"stale discovery", func(r *api.ObsInputHealth) {
			at := r.GeneratedAt.Add(-time.Hour)
			r.Jobs[5].Expectation.ObservedAt = &at
		}},
		{"missing count", func(r *api.ObsInputHealth) { r.Jobs[0].Targets.Counts.MissingMinimum = nil }},
		{"invalid partition", func(r *api.ObsInputHealth) { r.Jobs[0].Targets.Counts.Down = 1 }},
		{"count overflow", func(r *api.ObsInputHealth) { r.Jobs[0].Targets.Counts.Observed = 4097 }},
		{"false missing", func(r *api.ObsInputHealth) { v := 1; r.Jobs[0].Targets.Counts.MissingMinimum = &v }},
		{"metric count", func(r *api.ObsInputHealth) { r.Jobs[0].Metrics[0].Counts.Expected = 0 }},
		{"unsafe reason", func(r *api.ObsInputHealth) { r.Jobs[0].Metrics[0].Reason = "private failure" }},
		{"invented limitation", func(r *api.ObsInputHealth) { r.Limitations = []string{"private"} }},
		{"aggregate", func(r *api.ObsInputHealth) { r.State = "degraded" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, _ := json.Marshal(base)
			var copied api.ObsInputHealth
			if err := json.Unmarshal(data, &copied); err != nil {
				t.Fatal(err)
			}
			tc.change(&copied)
			if validObsInputHealth(copied, role) {
				t.Fatal("inconsistent response accepted")
			}
		})
	}
	for _, role := range operatorobs.InputHealthRoles() {
		if !validObsInputHealth(operatorobs.InputHealth(t.Context(), nil, role, time.Now().UTC()), role) {
			t.Fatal("unconfigured source rejected")
		}
	}
}
