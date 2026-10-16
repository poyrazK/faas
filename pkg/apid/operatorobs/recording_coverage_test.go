package operatorobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
	"github.com/onebox-faas/faas/pkg/promql"
)

func TestRecordingCoverageDefinitions(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		change      func(*promql.AlertRuleGroup)
	}{
		{"omitted empty labels", "matching", func(g *promql.AlertRuleGroup) { g.Rules[0].Labels = nil }},
		{"explicit empty labels", "matching", func(*promql.AlertRuleGroup) {}},
		{"null labels", "unknown", func(g *promql.AlertRuleGroup) { g.Rules[0].Labels = nil; g.Rules[0].LabelsNull = true }},
		{"changed expression", "changed", func(g *promql.AlertRuleGroup) { g.Rules[0].Query = "up == 1" }},
		{"changed labels", "changed", func(g *promql.AlertRuleGroup) { g.Rules[0].Labels = map[string]string{"severity": "warn"} }},
		{"failed evaluation", "matching", func(g *promql.AlertRuleGroup) {
			g.Rules[0].Health = "err"
			g.Rules[0].LastError = "private error"
			g.Rules[0].LastEvaluation = time.Time{}
		}},
		{"missing", "missing", func(g *promql.AlertRuleGroup) { g.Rules = []promql.AlertRule{} }},
		{"invalid syntax", "unknown", func(g *promql.AlertRuleGroup) { g.Rules[0].Query = "private invalid query" }},
		{"wrong type", "unknown", func(g *promql.AlertRuleGroup) { g.Rules[0].Type = "alerting" }},
		{"group labels", "unknown", func(g *promql.AlertRuleGroup) { g.Labels = map[string]string{"team": "private"} }},
		{"unattributed", "unknown", func(g *promql.AlertRuleGroup) { g.Name = "private\ngroup" }},
		{"duplicates", "ambiguous", func(g *promql.AlertRuleGroup) { g.Rules = append(g.Rules, g.Rules[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set, groups, base := coverageFixture(t, 1)
			d, err := obsrulecatalog.Normalize(groups[0].Rules[0].Query, 0, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			set.Rules[0].Definition = d
			r := &groups[0].Rules[0]
			r.Type = "recording"
			r.Duration = 0
			r.DurationReported = false
			r.KeepFiringFor = nil
			r.Labels = map[string]string{}
			tc.change(&groups[0])
			out, err := assessDefinitionCoverage(t.Context(), groups, set, base, "recording")
			if err != nil || len(out.Items) != 1 || out.Items[0].State != tc.state {
				t.Fatalf("recording assessment: %+v %v", out, err)
			}
			for _, field := range out.Items[0].ChangedFields {
				if field != "expression" && field != "labels" {
					t.Fatal("recording compared alert timers")
				}
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "severity") || strings.Contains(string(raw), "up{") {
				t.Fatalf("private source material leaked: %s", raw)
			}
		})
	}
}

func TestRecordingCoverageSelectionFailuresAndBounds(t *testing.T) {
	body := `{"status":"success","data":{"groups":[]}}`
	status := 200
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.String() != "/api/v1/rules?type=record" {
			t.Errorf("source: %s", r.URL)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	client := promql.NewClient(srv.URL, srv.Client())
	out := RecordingCoverage(t.Context(), client, "", time.Now().UTC())
	if calls != 0 || out.State != "unknown" || out.Reason != "expected_set_not_selected" || out.Dependencies != nil || out.Totals != nil || len(out.AvailableSets) != 2 {
		t.Fatal("expectation inferred without explicit selection")
	}
	out = RecordingCoverage(t.Context(), client, obsrulecatalog.CoreSetID, time.Now().UTC())
	if calls != 1 || out.State != "degraded" || out.Totals == nil || out.Totals.Missing != out.ExpectedSet.ExpectedRules || out.Totals.ObservedRules != 0 || out.Dependencies == nil || out.Dependencies.UncertainAlerts.TotalItems < out.Dependencies.UncertainAlerts.Limit || !out.Dependencies.UncertainAlerts.Truncated {
		t.Fatalf("empty source or dependency preview: %+v", out)
	}
	for _, invalid := range []string{`{"status":"success","warnings":["private"],"data":{"groups":[]}}`, `{"status":"success","infos":["private"],"data":{"groups":[]}}`, `{"status":"success","data":{}}`, `{"status":"success","data":{"groups":[{"name":"x"}]}}`, `{"status":"success","data":{"groups":[]}} {}`} {
		body = invalid
		out = RecordingCoverage(t.Context(), client, obsrulecatalog.FullSetID, time.Now().UTC())
		raw, _ := json.Marshal(out)
		if out.State != "unknown" || out.Reason != "source_unavailable" || out.Totals != nil || len(out.Items) != 0 || out.Dependencies == nil || strings.Contains(string(raw), "private") || strings.Contains(string(raw), srv.URL) {
			t.Fatalf("partial source fabricated coverage: %s", raw)
		}
	}
	status, body = 503, "private source error"
	out = RecordingCoverage(t.Context(), client, obsrulecatalog.CoreSetID, time.Now().UTC())
	if out.State != "unknown" || out.Totals != nil {
		t.Fatal("failed HTTP source became known missing")
	}
	out = RecordingCoverage(t.Context(), nil, obsrulecatalog.CoreSetID, time.Now().UTC())
	if out.Reason != "prometheus_not_configured" || out.Dependencies == nil {
		t.Fatal("unconfigured source lost independent expected syntax evidence")
	}
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	out = RecordingCoverage(ctx, promql.NewClient(slow.URL, slow.Client()).WithTimeout(10*time.Second), obsrulecatalog.CoreSetID, time.Now().UTC())
	if time.Since(start) > time.Second || out.Totals != nil || len(out.Items) != 0 || out.State != "unknown" {
		t.Fatal("unbounded or optimistic canceled read")
	}
	c, err := obsrulecatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	refs := []obsrulecatalog.RuleReference{}
	for _, r := range c.Sets[0].Rules {
		refs = append(refs, obsrulecatalog.Reference(r))
	}
	preview := recordingAlertReferences(refs, api.ObsRecordingDependentAlertsMax)
	if len(preview.Items) != api.ObsRecordingDependentAlertsMax || preview.TotalItems != len(refs) || !preview.Truncated || preview.Items[0].ID != refs[0].ID {
		t.Fatal("dependent alerts preview changed full totals or ordering")
	}
}

func TestRecordingCoverageMaximumMetadataFitsCLI(t *testing.T) {
	out := RecordingCoverage(t.Context(), nil, obsrulecatalog.FullSetID, time.Now().UTC())
	name := strings.Repeat("n", api.ObsRuleHealthIdentityMaxBytes)
	ref := api.ObsRecordingAlertReference{ID: strings.Repeat("1", 64), FileName: name, GroupName: name, RuleName: name}
	refs := api.ObsRecordingAlertReferences{Items: []api.ObsRecordingAlertReference{}, TotalItems: api.ObsAlertSnapshotMaxRules, Limit: api.ObsRecordingDependentAlertsMax, Truncated: true}
	for range api.ObsRecordingDependentAlertsMax {
		refs.Items = append(refs.Items, ref)
	}
	out.Dependencies.UncertainAlerts = refs
	for range out.Limit {
		out.Items = append(out.Items, api.ObsRecordingCoverageItem{Comparison: api.ObsRuleCoverageItem{ID: ref.ID, FileName: name, GroupName: name, RuleName: name, State: "changed", Reason: "definition_changed", ExpectedFingerprint: strings.Repeat("1", 64), ObservedFingerprint: strings.Repeat("2", 64), ObservedCandidates: api.ObsAlertSnapshotMaxRules, ChangedFields: []string{"expression", "labels"}}, DependentAlerts: refs})
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil || len(raw) > api.ObsIncidentBundleResponseMaxBytes {
		t.Fatalf("maximum metadata exceeds CLI response cap: %d %v", len(raw), err)
	}
}
