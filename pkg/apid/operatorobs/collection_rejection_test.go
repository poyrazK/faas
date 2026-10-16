package operatorobs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCollectionRejectionHistoricalEvidenceSurvivesRecovery(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	groups := historyRules(now, true)
	groups[0].File = "/private-location/faas.rules.yml"
	groups[0].Rules[0].Query = "private_query_text"
	groups[0].Rules[0].Labels["private_label"] = "private_label_value"
	groups[0].Rules[0].LastError = "private_evaluation_error"
	groups[0].Rules[0].Health = "err"
	groups[0].Rules[0].Annotations["runbook_url"] = "https://private.example/token"
	var source atomic.Value
	source.Store(groups)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"groups": source.Load()}})
	}))
	defer srv.Close()
	client, store := promql.NewClient(srv.URL, srv.Client()), state.NewMemStore()
	if err := CollectAlertsOnce(t.Context(), store, client, now); err == nil {
		t.Fatal("failed evaluation accepted")
	}
	later := now.Add(30 * time.Second)
	source.Store(historyRules(later, false))
	if err := CollectAlertsOnce(t.Context(), store, client, later); err != nil {
		t.Fatal(err)
	}
	if live := RuleHealth(t.Context(), client, later); live.State != "healthy" {
		t.Fatalf("live health: %+v", live)
	}
	before := calls.Load()
	out, err := AlertCollectionHistory(t.Context(), store, true, now.Add(-time.Minute), later, true)
	if err != nil || out.FailureDetails == nil || len(out.FailureDetails.Items) != 1 {
		t.Fatalf("historical details: %+v %v", out, err)
	}
	failure := out.FailureDetails.Items[0]
	d := failure.Diagnostics
	if calls.Load() != before || !failure.AttemptedAt.Equal(now) || failure.DiagnosticsState != "recorded" || d == nil || d.Totals.Failed != 1 || len(d.Items) != 1 || d.Items[0].State != "failed" || !d.Items[0].LastEvaluatedAt.Equal(now) || !d.Items[0].HasEvaluationError || d.Items[0].ID != ruleHealthHash([]any{groups[0].File, groups[0].Name, groups[0].Rules[0].Name, groups[0].Rules[0].Query, groups[0].Rules[0].Duration, groups[0].Rules[0].Labels}) {
		t.Fatalf("wrong source or time: %+v", failure)
	}
	encoded, _ := json.Marshal(out)
	for _, private := range []string{"private_query_text", "private_label_value", "private_label", "private_evaluation_error", "private.example", "/private-location/"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("private material retained: %s", private)
		}
	}
	light, err := AlertCollectionHistory(t.Context(), store, false, now.Add(-time.Minute), later, false)
	if err != nil || light.FailureDetails != nil || light.Coverage.Failures != out.Coverage.Failures {
		t.Fatalf("optional details: %+v %v", light, err)
	}
}

func TestCollectionRejectionBoundsAndStates(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name   string
		groups []promql.AlertRuleGroup
		reason string
	}{
		{"empty", []promql.AlertRuleGroup{}, "no_alert_rules_observed"},
		{"invalid empty group", []promql.AlertRuleGroup{{Rules: []promql.AlertRule{}}}, "rule_diagnostics_present"},
		{"fallback", historyRules(now, false), "collector_validation_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := collectionRejection(tc.groups, now)
			if d.Reason != tc.reason || !api.ValidObsAlertCollectionRejection(now, d) {
				t.Fatalf("diagnostics: %+v", d)
			}
		})
	}
	groups := historyRules(now, false)
	base := groups[0].Rules[0]
	unhealthy := api.ObsRuleHealthItemsMax + 5
	for i := 0; i < unhealthy; i++ {
		r := base
		r.Name, r.LastError, r.Health = fmt.Sprintf("Failed%d", i), "private error", "err"
		groups[0].Rules = append(groups[0].Rules, r)
	}
	d := collectionRejection(groups, now)
	if d.Totals.Rules != unhealthy+1 || d.Totals.Healthy != 1 || d.Totals.Failed != unhealthy || d.TotalItems != unhealthy || len(d.Items) != d.Limit || !d.Truncated || !api.ValidObsAlertCollectionRejection(now, d) {
		t.Fatalf("full counts, bounded failures: %+v", d)
	}
	for _, item := range d.Items {
		if item.State != "failed" {
			t.Fatal("healthy item displaced failure")
		}
	}
}

func TestCollectionRejectionDuplicatesAndUnreadableSource(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, mode := range []string{"duplicate", "malformed", "warning", "unrepresentable_clock"} {
		t.Run(mode, func(t *testing.T) {
			groups := historyRules(now, false)
			if mode == "unrepresentable_clock" {
				groups[0].Rules[0].LastEvaluation = time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
			} else {
				groups[0].Rules = append(groups[0].Rules, groups[0].Rules[0])
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if mode == "malformed" {
					_, _ = w.Write([]byte(`{"status":"success","private":"payload"}`))
					return
				}
				payload := map[string]any{"status": "success", "data": map[string]any{"groups": groups}}
				if mode == "warning" {
					payload["warnings"] = []string{"private warning"}
				}
				_ = json.NewEncoder(w).Encode(payload)
			}))
			defer srv.Close()
			store := state.NewMemStore()
			if err := CollectAlertsOnce(t.Context(), store, promql.NewClient(srv.URL, srv.Client()), now); err == nil {
				t.Fatal("invalid source accepted")
			}
			out, err := AlertCollectionHistory(t.Context(), store, true, now.Add(-time.Hour), now, true)
			if err != nil || out.FailureDetails == nil || len(out.FailureDetails.Items) != 1 {
				t.Fatalf("failure: %+v %v", out, err)
			}
			f := out.FailureDetails.Items[0]
			if mode == "duplicate" {
				if f.Reason != "snapshot_rejected" || f.DiagnosticsState != "recorded" || f.Diagnostics == nil || f.Diagnostics.Totals.Invalid != 2 {
					t.Fatalf("duplicate rule rejection: %+v", f)
				}
			} else if mode == "unrepresentable_clock" {
				if f.Reason != "snapshot_rejected" || f.DiagnosticsState != "unknown" || f.Diagnostics != nil {
					t.Fatalf("unrepresentable diagnostics erased receipt: %+v", f)
				}
			} else if f.Reason != "source_read_failed" || f.DiagnosticsState != "unknown" || f.Diagnostics != nil {
				t.Fatalf("invented diagnostics for unreadable source: %+v", f)
			}
		})
	}
}
