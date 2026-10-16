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

func recurrenceCLIResponse(t *testing.T, now time.Time, seeded bool) api.ObsAlertRecurrence {
	t.Helper()
	store := state.NewMemStore()
	if seeded {
		rule, series := strings.Repeat("a", 64), strings.Repeat("b", 64)
		e := state.OperatorAlertEpisode{RuleKey: rule, SeriesID: series, Name: "FaasApiDown", Severity: "critical", StartedAt: now.Add(-10 * time.Minute)}
		e.ID = state.OperatorAlertEpisodeID(rule, series, e.StartedAt)
		snap := state.OperatorAlertSnapshot{ObservedAt: now.Add(-5 * time.Minute), Rules: []state.OperatorAlertRuleObservation{{Key: rule, EvaluatedAt: now.Add(-5 * time.Minute), EpisodeIDs: []string{e.ID}}}, Episodes: []state.OperatorAlertEpisode{e}}
		if err := store.RecordOperatorAlertSnapshot(t.Context(), snap); err != nil {
			t.Fatal(err)
		}
		snap.ObservedAt = now.Add(-4 * time.Minute)
		snap.Rules[0].EvaluatedAt, snap.Rules[0].EpisodeIDs, snap.Episodes = snap.ObservedAt, []string{}, []state.OperatorAlertEpisode{}
		if err := store.RecordOperatorAlertSnapshot(t.Context(), snap); err != nil {
			t.Fatal(err)
		}
		e.StartedAt = snap.ObservedAt // Confirmed zero gap remains a valid sample.
		e.ID = state.OperatorAlertEpisodeID(rule, series, e.StartedAt)
		snap.ObservedAt, snap.Rules[0].EvaluatedAt, snap.Rules[0].EpisodeIDs, snap.Episodes = now, now, []string{e.ID}, []state.OperatorAlertEpisode{e}
		if err := store.RecordOperatorAlertSnapshot(t.Context(), snap); err != nil {
			t.Fatal(err)
		}
	}
	out, err := operatorobs.AlertRecurrence(t.Context(), store, true, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCmdObsRecurrence(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	since := now.Add(-time.Hour).Format(time.RFC3339Nano)
	response := recurrenceCLIResponse(t, now, false)
	calls, mode := 0, "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != operatorobs.AlertRecurrencePath || r.URL.Query().Get("since") != since || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		if mode == "error" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("private upstream body"))
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	args := []string{"--since", since}
	if code := cmdObsDispatch(append([]string{subObsRecurrence}, args...)); code != 0 || !strings.Contains(stdout.String(), "totals=unknown") || strings.Contains(stdout.String(), "repeat_activations=0") {
		t.Fatalf("unknown: %d %s %s", code, stdout, stderr)
	}
	response = recurrenceCLIResponse(t, now, true)
	stdout.Reset()
	if code := cmdObsRecurrence(append(append([]string{}, args...), "--json")); code != 0 {
		t.Fatalf("json: %d %s", code, stderr)
	}
	var got api.ObsAlertRecurrence
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Totals.Metrics.RecoveryGap.MinSeconds == nil || *got.Totals.Metrics.RecoveryGap.MinSeconds != 0 || got.Items[0].RecentEpisodes[0].ResolvedAt != nil {
		t.Fatalf("zero and null: %s %v", stdout, err)
	}
	stdout.Reset()
	if code := cmdObsRecurrence(args); code != 0 || !strings.Contains(stdout.String(), "repeat_activations=1") || !strings.Contains(stdout.String(), "recovery_gap samples=1") || !strings.Contains(stdout.String(), "collection_coverage=partial") || !strings.Contains(stdout.String(), "evidence=/v1/admin/obs/") {
		t.Fatalf("evidence output: %d %s %s", code, stdout, stderr)
	}
	before := calls
	for _, bad := range [][]string{{"--since", "bad"}, {"--timeout", "0s"}, {"extra"}, {"--limit", "5"}} {
		if code := cmdObsRecurrence(bad); code != 2 {
			t.Fatalf("invalid args: %d", code)
		}
	}
	if calls != before {
		t.Fatal("invalid arguments sent requests")
	}
	response.Totals.Metrics.RecoveryGap.Samples++
	stdout.Reset()
	if code := cmdObsRecurrence(args); code != 1 || stdout.Len() != 0 {
		t.Fatalf("inconsistent evidence accepted: %d %s", code, stdout)
	}
	mode = "error"
	stderr.Reset()
	if code := cmdObsRecurrence(args); code != 1 || stdout.Len() != 0 || strings.Contains(stderr.String(), "private upstream body") {
		t.Fatalf("redaction: %d %s %s", code, stdout, stderr)
	}
}
