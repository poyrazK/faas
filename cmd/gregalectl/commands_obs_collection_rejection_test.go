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
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCmdObsCollectionHistoryDetails(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	store := state.NewMemStore()
	d := api.ObsAlertCollectionRejection{Scope: "rejected_snapshot", Reason: "no_alert_rules_observed", Items: []api.ObsRuleHealthItem{}, Limit: api.ObsAlertCollectionRejectionItemsMax}
	if err := store.RecordOperatorAlertFailure(t.Context(), now.Add(-time.Second), "timeout"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertRejection(t.Context(), now, d); err != nil {
		t.Fatal(err)
	}
	out, err := operatorobs.AlertCollectionHistory(t.Context(), store, true, now.Add(-time.Hour), now, true)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("details") != "true" {
			t.Errorf("details query missing: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	if code := cmdObsCollectionHistory([]string{"--details"}); code != 0 || !strings.Contains(stdout.String(), "diagnostics=recorded") || !strings.Contains(stdout.String(), "diagnostics=unknown") || !strings.Contains(stdout.String(), "reason=no_alert_rules_observed") {
		t.Fatalf("details: %d %s %s", code, stdout, stderr)
	}
	stdout.Reset()
	if code := cmdObsCollectionHistory([]string{"--details", "--json"}); code != 0 {
		t.Fatalf("JSON: %d %s", code, stderr)
	}
	var got api.ObsAlertCollectionHistory
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.FailureDetails == nil || got.FailureDetails.TotalFailures != 2 {
		t.Fatalf("JSON details: %s %v", stdout, err)
	}
	baseline, _ := json.Marshal(out)
	for _, tc := range []struct {
		name   string
		mutate func(*api.ObsAlertCollectionHistory)
	}{
		{"missing", func(o *api.ObsAlertCollectionHistory) { o.FailureDetails = nil }},
		{"false recorded", func(o *api.ObsAlertCollectionHistory) { o.FailureDetails.Items[1].DiagnosticsState = "recorded" }},
		{"missing diagnostic", func(o *api.ObsAlertCollectionHistory) { o.FailureDetails.Items[0].Diagnostics = nil }},
		{"wrong phase", func(o *api.ObsAlertCollectionHistory) { o.FailureDetails.Items[0].Reason = "timeout" }},
		{"bad totals", func(o *api.ObsAlertCollectionHistory) { o.FailureDetails.TotalFailures++ }},
		{"bad bound", func(o *api.ObsAlertCollectionHistory) { o.FailureDetails.Limit++ }},
		{"bad truncation", func(o *api.ObsAlertCollectionHistory) { o.FailureDetails.Truncated = true }},
		{"bad chronology", func(o *api.ObsAlertCollectionHistory) { o.FailureDetails.Items[1].AttemptedAt = now }},
		{"future", func(o *api.ObsAlertCollectionHistory) { o.FailureDetails.Items[0].AttemptedAt = now.Add(time.Second) }},
		{"raw reason", func(o *api.ObsAlertCollectionHistory) { o.FailureDetails.Items[0].Diagnostics.Reason = "private_error" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out = api.ObsAlertCollectionHistory{}
			if err := json.Unmarshal(baseline, &out); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&out)
			stdout.Reset()
			stderr.Reset()
			if code := cmdObsCollectionHistory([]string{"--details", "--json"}); code != 1 || stdout.Len() != 0 || strings.Contains(stderr.String(), "private_error") {
				t.Fatalf("invalid details: %d %s %s", code, stdout, stderr)
			}
		})
	}
}
