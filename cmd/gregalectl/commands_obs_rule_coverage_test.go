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
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
	"github.com/onebox-faas/faas/pkg/promql"
)

func coverageTestResponse(t *testing.T) api.ObsRuleCoverage {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"groups":[]}}`))
	}))
	defer srv.Close()
	return operatorobs.RuleCoverage(t.Context(), promql.NewClient(srv.URL, srv.Client()), obsrulecatalog.CoreSetID, time.Now().UTC())
}

func TestCmdObsRuleCoverage(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	response := operatorobs.RuleCoverage(t.Context(), nil, "", time.Now().UTC())
	selected := ""
	calls := 0
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != operatorobs.RuleCoveragePath || r.URL.Query().Get("expected_set") != selected || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		if mode == "error" {
			http.Error(w, "private upstream body", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	if code := cmdObsDispatch([]string{subObsRuleCoverage}); code != 0 || !strings.Contains(out.String(), "totals=unknown") || !strings.Contains(out.String(), "available_set=gregale-core-v1") {
		t.Fatalf("unselected: %d %s %s", code, out, stderr)
	}
	response = coverageTestResponse(t)
	selected = obsrulecatalog.CoreSetID
	out.Reset()
	if code := cmdObsRuleCoverage([]string{"--expected-set", selected}); code != 0 || !strings.Contains(out.String(), "rule_coverage=degraded") || !strings.Contains(out.String(), "state=missing") || !strings.Contains(out.String(), "evaluation_metric_and_notification_health=unverified") {
		t.Fatalf("missing inventory: %d %s %s", code, out, stderr)
	}
	out.Reset()
	if code := cmdObsRuleCoverage([]string{"--expected-set", selected, "--json"}); code != 0 || !strings.Contains(out.String(), `"selection_source": "explicit_request"`) || !strings.Contains(out.String(), `"changed_fields": []`) {
		t.Fatalf("JSON: %d %s %s", code, out, stderr)
	}
	before := calls
	for _, args := range [][]string{{"--expected-set", ""}, {"--expected-set", "unknown"}, {"--expected-set", selected, "--expected-set", selected}, {"--timeout", "0s"}, {"extra"}, {"--since", "now"}} {
		if code := cmdObsRuleCoverage(args); code != 2 {
			t.Fatalf("invalid args: %v code=%d", args, code)
		}
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	if code := cmdObsRuleCoverage(nil); code != 2 || calls != before {
		t.Fatal("bad flags or missing token made request")
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	mode = "error"
	out.Reset()
	stderr.Reset()
	if code := cmdObsRuleCoverage([]string{"--expected-set", selected}); code != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "private") {
		t.Fatalf("source error redaction: %d %s %s", code, out, stderr)
	}
	mode = "ok"
	response.Items[0].Reason = "private failure"
	out.Reset()
	stderr.Reset()
	if code := cmdObsRuleCoverage([]string{"--expected-set", selected, "--json"}); code != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "private") {
		t.Fatalf("invalid response leaked: %d %s %s", code, out, stderr)
	}
}

func TestCmdObsRuleCoverageRejectsInconsistentEvidence(t *testing.T) {
	base := coverageTestResponse(t)
	if !validObsRuleCoverage(base, obsrulecatalog.CoreSetID) {
		t.Fatal("valid server comparison rejected")
	}
	for _, tc := range []struct {
		name   string
		change func(*api.ObsRuleCoverage)
	}{
		{"selection", func(r *api.ObsRuleCoverage) { r.SelectionSource = "none" }},
		{"wrong profile", func(r *api.ObsRuleCoverage) { r.ExpectedSet.ID = obsrulecatalog.FullSetID }},
		{"revision", func(r *api.ObsRuleCoverage) { r.ExpectedSet.Revision = strings.Repeat("0", 64) }},
		{"counts", func(r *api.ObsRuleCoverage) { r.Totals.Missing-- }},
		{"false covered", func(r *api.ObsRuleCoverage) { r.State = "covered"; r.Reason = "" }},
		{"truncation", func(r *api.ObsRuleCoverage) { r.Truncated = false }},
		{"nil items", func(r *api.ObsRuleCoverage) { r.Items = nil }},
		{"raw path", func(r *api.ObsRuleCoverage) { r.Items[0].FileName = "/private/faas.rules.yml" }},
		{"duplicate", func(r *api.ObsRuleCoverage) { r.Items[1] = r.Items[0] }},
		{"ordering", func(r *api.ObsRuleCoverage) { r.Items[0], r.Items[1] = r.Items[1], r.Items[0] }},
		{"missing with observation", func(r *api.ObsRuleCoverage) { r.Items[0].ObservedCandidates = 1 }},
		{"missing with digest", func(r *api.ObsRuleCoverage) { r.Items[0].ObservedFingerprint = strings.Repeat("1", 64) }},
		{"open changed field", func(r *api.ObsRuleCoverage) { r.Items[0].ChangedFields = []string{"private"} }},
		{"open limitations", func(r *api.ObsRuleCoverage) { r.Limitations = []string{"private"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, _ := json.Marshal(base)
			var copy api.ObsRuleCoverage
			if err := json.Unmarshal(encoded, &copy); err != nil {
				t.Fatal(err)
			}
			tc.change(&copy)
			if validObsRuleCoverage(copy, obsrulecatalog.CoreSetID) {
				t.Fatal("inconsistent evidence accepted")
			}
		})
	}
	item := base.Items[0]
	item.State, item.Reason, item.ObservedCandidates = "changed", "definition_changed", 1
	item.ObservedFingerprint, item.ChangedFields = strings.Repeat("1", 64), []string{"expression", "for", "keep_firing_for", "labels"}
	if !validCoverageItem(item, base.ExpectedSet.Files) {
		t.Fatal("valid changed definition rejected")
	}
	item.ChangedFields = []string{"labels", "expression"}
	if validCoverageItem(item, base.ExpectedSet.Files) {
		t.Fatal("invalid change order accepted")
	}
}
