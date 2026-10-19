package operatorobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
	"github.com/onebox-faas/faas/pkg/promql"
)

func coverageFixture(t *testing.T, count int) (obsrulecatalog.Set, []promql.AlertRuleGroup, api.ObsRuleCoverage) {
	t.Helper()
	definition, err := obsrulecatalog.Normalize(`up{job="private-target"} == 0`, 60, 30, map[string]string{"severity": "critical"})
	if err != nil {
		t.Fatal(err)
	}
	set := obsrulecatalog.Set{ID: obsrulecatalog.CoreSetID, Files: []string{"faas.rules.yml"}}
	groups := []promql.AlertRuleGroup{{File: "/private-directory/faas.rules.yml", Name: "faas", Rules: []promql.AlertRule{}}}
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("Example%04dDown", i)
		id, ok := obsrulecatalog.Identity("faas.rules.yml", "faas", name)
		if !ok {
			t.Fatal("fixture identity")
		}
		set.Rules = append(set.Rules, obsrulecatalog.Rule{ID: id, FileName: "faas.rules.yml", GroupName: "faas", RuleName: name, Definition: definition})
		keep := 30.0
		groups[0].Rules = append(groups[0].Rules, promql.AlertRule{Type: "alerting", Name: name, Query: `up{job="private-target"} == 0`, Duration: 60, DurationReported: true, KeepFiringFor: &keep, Labels: map[string]string{"severity": "critical"}, Alerts: []promql.Alert{}, Health: "ok", State: "inactive", LastEvaluation: time.Now().UTC()})
	}
	out := RuleCoverage(t.Context(), nil, "", time.Now().UTC())
	out.ExpectedSet = &api.ObsRuleCoverageSet{ID: set.ID, Files: set.Files, ExpectedRules: count}
	return set, groups, out
}

func TestRuleCoverageMissingChangedAndUncertainDefinitions(t *testing.T) {
	for _, tc := range []struct {
		name, state, reason string
		change              func(*promql.AlertRuleGroup)
	}{
		{"matching", "matching", "", func(*promql.AlertRuleGroup) {}},
		{"relocated file", "matching", "", func(g *promql.AlertRuleGroup) { g.File = "/another-directory/faas.rules.yml" }},
		{"failed evaluation", "matching", "", func(g *promql.AlertRuleGroup) {
			g.Rules[0].Health = "err"
			g.Rules[0].LastError = "private error"
			g.Rules[0].LastEvaluation = time.Time{}
		}},
		{"missing", "missing", "rule_missing", func(g *promql.AlertRuleGroup) { g.Rules = []promql.AlertRule{} }},
		{"changed expression", "changed", "definition_changed", func(g *promql.AlertRuleGroup) { g.Rules[0].Query = "up == 1" }},
		{"changed hold", "changed", "definition_changed", func(g *promql.AlertRuleGroup) { g.Rules[0].Duration++ }},
		{"changed keep", "changed", "definition_changed", func(g *promql.AlertRuleGroup) { *g.Rules[0].KeepFiringFor++ }},
		{"changed labels", "changed", "definition_changed", func(g *promql.AlertRuleGroup) { g.Rules[0].Labels["severity"] = "warning" }},
		{"missing hold", "unknown", "definition_unavailable", func(g *promql.AlertRuleGroup) { g.Rules[0].DurationReported = false }},
		{"missing keep", "unknown", "definition_unavailable", func(g *promql.AlertRuleGroup) { g.Rules[0].KeepFiringFor = nil }},
		{"missing labels", "unknown", "definition_unavailable", func(g *promql.AlertRuleGroup) { g.Rules[0].Labels = nil }},
		{"invalid syntax", "unknown", "definition_unavailable", func(g *promql.AlertRuleGroup) { g.Rules[0].Query = "private invalid query" }},
		{"negative hold", "unknown", "definition_unavailable", func(g *promql.AlertRuleGroup) { g.Rules[0].Duration = -1 }},
		{"wrong type", "unknown", "definition_unavailable", func(g *promql.AlertRuleGroup) { g.Rules[0].Type = "recording" }},
		{"group labels", "unknown", "definition_unavailable", func(g *promql.AlertRuleGroup) { g.Labels = map[string]string{"team": "private"} }},
		{"unattributed rule", "unknown", "source_identity_unavailable", func(g *promql.AlertRuleGroup) { g.Rules[0].Name = "private\nname" }},
		{"unattributed group", "unknown", "source_identity_unavailable", func(g *promql.AlertRuleGroup) { g.Name = "private\ngroup" }},
		{"duplicate candidate", "ambiguous", "ambiguous_identity", func(g *promql.AlertRuleGroup) { g.Rules = append(g.Rules, g.Rules[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set, groups, base := coverageFixture(t, 1)
			tc.change(&groups[0])
			out, err := assessRuleCoverage(t.Context(), groups, set, base)
			if err != nil || len(out.Items) != 1 || out.Items[0].State != tc.state || out.Items[0].Reason != tc.reason {
				t.Fatalf("wrong comparison: %+v %v", out, err)
			}
			if tc.state == "matching" && out.State != "covered" || tc.state == "unknown" && out.State != "unknown" || (tc.state == "changed" || tc.state == "missing" || tc.state == "ambiguous") && out.State != "degraded" {
				t.Fatalf("root state: %s", out.State)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "up{") || strings.Contains(string(encoded), "severity") {
				t.Fatalf("private material leaked: %s", encoded)
			}
		})
	}
}

func TestRuleCoverageAmbiguityAndMinimumInventory(t *testing.T) {
	for _, emptyDuplicate := range []bool{false, true} {
		set, groups, base := coverageFixture(t, 1)
		duplicate := groups[0]
		duplicate.File = "/other-directory/faas.rules.yml"
		if emptyDuplicate {
			duplicate.Rules = []promql.AlertRule{}
		}
		groups = append(groups, duplicate)
		out, err := assessRuleCoverage(t.Context(), groups, set, base)
		if err != nil || out.Totals.Ambiguous != 1 || out.Items[0].State != "ambiguous" {
			t.Fatalf("basename/group collision hidden: %+v %v", out, err)
		}
	}
	set, groups, base := coverageFixture(t, 1)
	additional := groups[0]
	additional.File = "/other/companion.rules.yml"
	groups = append(groups, additional)
	out, err := assessRuleCoverage(t.Context(), groups, set, base)
	if err != nil || out.State != "covered" || out.Totals.AdditionalRules != 1 || out.Totals.Matching != 1 {
		t.Fatalf("minimum expectation rejected additional rule: %+v %v", out, err)
	}
	groups = append(groups, promql.AlertRuleGroup{Name: "private invalid", Rules: []promql.AlertRule{}})
	out, err = assessRuleCoverage(t.Context(), groups, set, base)
	if err != nil || out.State != "partial" || out.Totals.Matching != 1 || out.Totals.InvalidObservedGroups != 1 {
		t.Fatalf("unreadable identity was called covered: %+v %v", out, err)
	}
}

func TestRuleCoverageCriticalGapFullTotalsAndPriority(t *testing.T) {
	set, groups, base := coverageFixture(t, api.ObsRuleCoverageItemsMax+5)
	// A quiet source can be missing the last critical rule. Totals must cover it
	// before the matching preview is truncated.
	groups[0].Rules = groups[0].Rules[:len(groups[0].Rules)-1]
	groups[0].Rules[0].Query = "up == 1"
	groups[0].Rules[0].Duration++
	*groups[0].Rules[0].KeepFiringFor++
	groups[0].Rules[0].Labels = map[string]string{"severity": "warning"}
	out, err := assessRuleCoverage(t.Context(), groups, set, base)
	if err != nil || out.State != "degraded" || out.Totals.Missing != 1 || out.Totals.Changed != 1 || out.Totals.Matching != len(set.Rules)-2 || !out.Truncated || len(out.Items) != api.ObsRuleCoverageItemsMax || out.TotalItems != len(set.Rules) || out.Items[0].State != "changed" || out.Items[1].State != "missing" {
		t.Fatalf("critical gap lost behind healthy preview: %+v %v", out, err)
	}
	if !reflect.DeepEqual(out.Items[0].ChangedFields, []string{"expression", "for", "keep_firing_for", "labels"}) {
		t.Fatalf("changed fields: %v", out.Items[0].ChangedFields)
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil || len(encoded) > api.ObsIncidentBundleResponseMaxBytes {
		t.Fatal("preview exceeds CLI response budget")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := assessRuleCoverage(ctx, groups, set, base); err == nil {
		t.Fatal("canceled comparison returned complete evidence")
	}
}

func TestRuleCoverageExplicitSelectionAndSourceFailure(t *testing.T) {
	body := `{"status":"success","data":{"groups":[]}}`
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.String() != "/api/v1/rules?type=alert" {
			t.Errorf("unexpected source: %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	client := promql.NewClient(srv.URL, srv.Client())
	out := RuleCoverage(t.Context(), client, "", time.Now().UTC())
	if calls != 0 || out.State != "unknown" || out.Reason != "expected_set_not_selected" || out.Totals != nil || len(out.AvailableSets) != 2 {
		t.Fatalf("inferred expectation: %+v calls=%d", out, calls)
	}
	out = RuleCoverage(t.Context(), client, obsrulecatalog.CoreSetID, time.Now().UTC())
	if calls != 1 || out.State != "degraded" || out.ExpectedSet == nil || out.Totals == nil || out.Totals.Missing != out.ExpectedSet.ExpectedRules || out.Totals.Matching != 0 || out.SelectionSource != "explicit_request" {
		t.Fatalf("empty selected source became unknown or covered: %+v", out)
	}
	for _, invalid := range []string{
		`{"status":"success","warnings":["private warning"],"data":{"groups":[]}}`,
		`{"status":"success","infos":["private info"],"data":{"groups":[]}}`,
		`{"status":"success","data":{}}`,
		`{"status":"success","data":{"groups":[{"name":"x"}]}}`,
		`{"status":"success","data":{"groups":[]}} {}`,
	} {
		body = invalid
		out = RuleCoverage(t.Context(), client, obsrulecatalog.FullSetID, time.Now().UTC())
		encoded, _ := json.Marshal(out)
		if out.State != "unknown" || out.Reason != "source_unavailable" || out.Totals != nil || len(out.Items) != 0 || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), srv.URL) {
			t.Fatalf("partial source became comparison: %s", encoded)
		}
	}
	out = RuleCoverage(t.Context(), nil, obsrulecatalog.CoreSetID, time.Now().UTC())
	if out.State != "unknown" || out.Reason != "prometheus_not_configured" || out.ExpectedSet == nil || out.Totals != nil {
		t.Fatalf("unconfigured: %+v", out)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	out = RuleCoverage(ctx, promql.NewClient(slow.URL, slow.Client()).WithTimeout(10*time.Second), obsrulecatalog.CoreSetID, time.Now().UTC())
	if time.Since(start) > time.Second || out.State != "unknown" || out.Totals != nil || len(out.Items) != 0 {
		t.Fatalf("deadline returned partial evidence: %+v", out)
	}
}

func TestRuleCoverageSelection(t *testing.T) {
	for _, raw := range []string{"expected_set=", "expected_set=unknown", "expected_set=gregale-core-v1&expected_set=gregale-full-v1", "expected_set=gregale-core-v1&limit=1", "other=1", "expected_set=%zz", ";", "&"} {
		if _, err := RuleCoverageSelection(raw, false); err == nil {
			t.Fatalf("invalid selection accepted: %s", raw)
		}
	}
	if _, err := RuleCoverageSelection("", true); err == nil {
		t.Fatal("dangling query accepted")
	}
	for _, id := range []string{"", obsrulecatalog.CoreSetID, obsrulecatalog.FullSetID} {
		raw := ""
		if id != "" {
			raw = "expected_set=" + id
		}
		if got, err := RuleCoverageSelection(raw, false); err != nil || got != id {
			t.Fatalf("selection: %q %v", got, err)
		}
	}
}
