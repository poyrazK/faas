package operatorobs

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func TestRuleHealthAssessmentMatchesCollectorRejection(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name, want string
		change     func(*promql.AlertRuleGroup)
	}{
		{"quiet", "healthy", func(*promql.AlertRuleGroup) {}},
		{"failed", "failed", func(g *promql.AlertRuleGroup) {
			g.Rules[0].Health = "err"
			g.Rules[0].LastError = "private execution error"
		}},
		{"error despite ok", "failed", func(g *promql.AlertRuleGroup) { g.Rules[0].LastError = "private execution error" }},
		{"unknown evaluation", "unavailable", func(g *promql.AlertRuleGroup) { g.Rules[0].Health = "private health string" }},
		{"missing evaluation", "unavailable", func(g *promql.AlertRuleGroup) { g.Rules[0].LastEvaluation = time.Time{} }},
		{"future clock", "invalid_clock", func(g *promql.AlertRuleGroup) {
			g.Rules[0].LastEvaluation = now.Add(api.ObsHealthClockSkewTolerance + time.Microsecond)
		}},
		{"skew boundary", "healthy", func(g *promql.AlertRuleGroup) { g.Rules[0].LastEvaluation = now.Add(api.ObsHealthClockSkewTolerance) }},
		{"stale", "stale", func(g *promql.AlertRuleGroup) {
			g.Rules[0].LastEvaluation = now.Add(-api.ObsHealthFreshnessMaxAge - time.Microsecond)
		}},
		{"freshness boundary", "healthy", func(g *promql.AlertRuleGroup) { g.Rules[0].LastEvaluation = now.Add(-api.ObsHealthFreshnessMaxAge) }},
		{"query absent", "invalid", func(g *promql.AlertRuleGroup) { g.Rules[0].Query = "" }},
		{"negative duration", "invalid", func(g *promql.AlertRuleGroup) { g.Rules[0].Duration = -1 }},
		{"nonfinite duration", "invalid", func(g *promql.AlertRuleGroup) { g.Rules[0].Duration = math.NaN() }},
		{"invalid name", "invalid", func(g *promql.AlertRuleGroup) { g.Rules[0].Name = "private@example.test" }},
		{"unexpected type", "invalid", func(g *promql.AlertRuleGroup) { g.Rules[0].Type = "recording" }},
		{"missing alert array", "invalid", func(g *promql.AlertRuleGroup) { g.Rules[0].Alerts = nil }},
		{"inconsistent state", "invalid", func(g *promql.AlertRuleGroup) { g.Rules[0].State = "firing" }},
		{"unknown state", "invalid", func(g *promql.AlertRuleGroup) { g.Rules[0].State = "private state" }},
		{"missing group name", "invalid", func(g *promql.AlertRuleGroup) { g.Name = "" }},
		{"missing group file", "invalid", func(g *promql.AlertRuleGroup) { g.File = "" }},
		{"pending", "healthy", func(g *promql.AlertRuleGroup) {
			g.Rules[0].State = "pending"
			g.Rules[0].Alerts = []promql.Alert{{State: "pending", ActiveAt: now.Add(-time.Second), Labels: map[string]string{"alertname": g.Rules[0].Name}}}
		}},
		{"firing", "healthy", func(g *promql.AlertRuleGroup) {
			g.Rules[0].State = "firing"
			g.Rules[0].Alerts = []promql.Alert{{State: "firing", ActiveAt: now.Add(-time.Minute), Labels: map[string]string{"alertname": g.Rules[0].Name}}}
		}},
		{"invalid alert state", "invalid", func(g *promql.AlertRuleGroup) {
			g.Rules[0].Alerts = []promql.Alert{{State: "private state", ActiveAt: now.Add(-time.Second), Labels: map[string]string{"alertname": g.Rules[0].Name}}}
		}},
		{"invalid alert clock", "invalid", func(g *promql.AlertRuleGroup) {
			g.Rules[0].Alerts = []promql.Alert{{State: "pending", ActiveAt: now.Add(time.Second), Labels: map[string]string{"alertname": g.Rules[0].Name}}}
		}},
		{"invalid alert name", "invalid", func(g *promql.AlertRuleGroup) {
			g.Rules[0].Alerts = []promql.Alert{{State: "pending", ActiveAt: now.Add(-time.Second), Labels: map[string]string{"alertname": "different"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groups := historyRules(now, false)
			tc.change(&groups[0])
			out := assessRuleHealth(groups, RuleHealth(t.Context(), nil, now), now)
			if d := collectionRejection(groups, now); !api.ValidObsAlertCollectionRejection(now, d) {
				t.Fatalf("unpersistable rejection diagnostics: %+v", d)
			}
			item := out.Items[0]
			_, err := alertSnapshot(groups, now)
			if item.State != tc.want || (err == nil) != (out.State == "healthy") {
				t.Fatalf("diagnostics diverged from collector: %+v collector=%v", out, err)
			}
			if out.Totals.Rules != 1 || out.Totals.Groups != 1 || item.Issues == nil || item.GroupID == "" || item.ID == "" {
				t.Fatalf("metadata: %+v", out)
			}
			if tc.want == "healthy" {
				if item.Firing == nil || item.Pending == nil || len(item.Issues) != 0 {
					t.Fatalf("healthy counts: %+v", item)
				}
				if tc.name == "quiet" && (*item.Firing != 0 || *item.Pending != 0) {
					t.Fatal("quiet counters")
				}
				if tc.name == "pending" && (*item.Pending != 1 || *item.Firing != 0) {
					t.Fatal("pending counter")
				}
				if tc.name == "firing" && (*item.Firing != 1 || *item.Pending != 0) {
					t.Fatal("firing counter")
				}
			} else if item.Firing != nil || item.Pending != nil || len(item.Issues) == 0 {
				t.Fatalf("unhealthy appears quiet: %+v", item)
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private") || strings.Contains(string(body), "/etc/prometheus/") || strings.Contains(string(body), "up == 0") {
				t.Fatalf("private rule material leaked: %s", body)
			}
		})
	}
}

func TestRuleHealthEmptyAndAmbiguousIdentities(t *testing.T) {
	now := time.Now().UTC()
	for _, groups := range [][]promql.AlertRuleGroup{{}, {{Name: "empty", File: "rules.yml", Rules: []promql.AlertRule{}}}} {
		out := assessRuleHealth(groups, RuleHealth(t.Context(), nil, now), now)
		if out.State != "unavailable" || out.Reason != "no_alert_rules_observed" || out.Totals == nil || out.Totals.Rules != 0 || out.Items == nil {
			t.Fatalf("empty source looked green: %+v", out)
		}
	}
	groups := []promql.AlertRuleGroup{{Rules: []promql.AlertRule{}}}
	out := assessRuleHealth(groups, RuleHealth(t.Context(), nil, now), now)
	if out.State != "degraded" || out.Totals.InvalidGroups != 1 || len(out.Items) != 1 || out.Items[0].Kind != "group" {
		t.Fatalf("empty invalid group hidden: %+v", out)
	}
	groups = historyRules(now, false)
	groups[0].Rules = append(groups[0].Rules, groups[0].Rules[0])
	out = assessRuleHealth(groups, RuleHealth(t.Context(), nil, now), now)
	if out.State != "degraded" || out.Totals.Invalid != 2 || !slices.Contains(out.Items[0].Issues, "duplicate_rule_identity") {
		t.Fatalf("duplicate rule hidden: %+v", out)
	}
	groups = historyRules(now, false)
	groups = append(groups, groups[0])
	out = assessRuleHealth(groups, RuleHealth(t.Context(), nil, now), now)
	if out.State != "degraded" || out.Totals.InvalidGroups != 2 || out.Totals.Invalid != 2 {
		t.Fatalf("duplicate group hidden: %+v", out)
	}
}

func TestRuleHealthCompleteTotalsBoundedPreviewAndPrivacy(t *testing.T) {
	now := time.Now().UTC()
	groups := historyRules(now, false)
	groups[0].File = "/private-directory/faas.rules.yml"
	groups[0].Name = "private\ngroup"
	base := groups[0].Rules[0]
	base.Query = "private query"
	base.Labels = map[string]string{"email": "private@example.test"}
	groups[0].Rules = nil
	for i := 0; i < api.ObsRuleHealthItemsMax+1; i++ {
		rule := base
		rule.Name = fmt.Sprintf("FaasRule%d", i)
		if i == api.ObsRuleHealthItemsMax {
			rule.Health = "err"
			rule.LastError = "private error"
		}
		groups[0].Rules = append(groups[0].Rules, rule)
	}
	out := assessRuleHealth(groups, RuleHealth(t.Context(), nil, now), now)
	if out.State != "degraded" || !out.Truncated || len(out.Items) != api.ObsRuleHealthItemsMax || out.TotalItems != api.ObsRuleHealthItemsMax+1 || out.Totals.Failed != 1 || out.Totals.Healthy != api.ObsRuleHealthItemsMax || out.Items[0].State != "failed" {
		t.Fatalf("preview hid failure or changed totals: %+v", out)
	}
	body, _ := json.Marshal(out)
	if strings.Contains(string(body), "private") || strings.Contains(string(body), "email") {
		t.Fatalf("privacy: %s", body)
	}
	// Worst-case metadata stays inside one bundle response budget, including
	// owner-controlled runbook links. Invalid/oversized text is omitted.
	groups[0].Name = strings.Repeat("g", api.ObsRuleHealthIdentityMaxBytes)
	groups[0].File = "/hidden/" + strings.Repeat("f", api.ObsRuleHealthIdentityMaxBytes)
	for i := range groups[0].Rules {
		groups[0].Rules[i].Name = fmt.Sprintf("%s%04d", strings.Repeat("R", api.ObsAlertNameMaxBytes-4), i)
		groups[0].Rules[i].Annotations = map[string]string{"runbook_url": "/docs/" + strings.Repeat("a", api.ObsRuleHealthRunbookMaxBytes-6)}
	}
	out = assessRuleHealth(groups, RuleHealth(t.Context(), nil, now), now)
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil || len(body) > api.ObsIncidentBundleResponseMaxBytes {
		t.Fatalf("unbounded metadata: %d %v", len(body), err)
	}
}

func TestRuleHealthLiveEnvelopeAndCancellation(t *testing.T) {
	now := time.Now().UTC()
	for _, body := range []string{
		`{"status":"success","warnings":["private warning"],"data":{"groups":[]}}`,
		`{"status":"success","data":{}}`,
		`{"status":"success","data":{"groups":[{"name":"x","file":"rules.yml"}]}}`,
		`{"status":"success","data":{"groups":[{"rules":[{"lastEvaluation":"private"}]}]}}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		out := RuleHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
		srv.Close()
		encoded, _ := json.Marshal(out)
		if out.State != "unavailable" || out.Totals != nil || len(out.Items) != 0 || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), srv.URL) {
			t.Fatalf("invalid source: %s", encoded)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	out := RuleHealth(ctx, promql.NewClient(srv.URL, srv.Client()).WithTimeout(10*time.Second), now)
	if time.Since(start) > time.Second || out.State != "unavailable" || out.Totals != nil {
		t.Fatalf("unbounded or optimistic canceled read: %+v", out)
	}
}
