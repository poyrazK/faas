package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func deploymentLoopFixture(e api.ObsRuntimeBuilds, state string) *operatorobs.RuntimeLoopHealth {
	h := &operatorobs.RuntimeLoopHealth{RuntimeLoopSummary: operatorobs.RuntimeLoopSummary{State: "no_reported_stall"}, Items: []operatorobs.RuntimeLoopItem{}}
	for i, job := range e.Jobs {
		names := []string{"runtime"}
		if job.Daemon == "schedd" {
			names = append(names, "main")
		}
		if job.Daemon == "vmmd" {
			names = append(names, "sweep")
		}
		h.Counts.Expected += job.Expected * len(names)
		for _, loop := range names {
			reported, age := false, float64(1)
			at := e.GeneratedAt
			h.Items = append(h.Items, operatorobs.RuntimeLoopItem{Job: job.Job, Daemon: job.Daemon, Loop: loop, TargetID: e.Items[i].TargetID, Node: e.Items[i].Node, State: "no_reported_stall", ReportedStalled: &reported, AgeSeconds: &age, ScrapeObservedAt: &at, StallObservedAt: &at, AgeObservedAt: &at})
			h.Counts.Observed++
			h.Counts.NoReportedStall++
		}
	}
	if state != "no_reported_stall" {
		first := &h.Items[0]
		value := true
		first.ReportedStalled = &value
		first.State = "stalled"
		h.State, h.Reason = "stalled", "reported_loop_stall"
		h.Counts.NoReportedStall--
		h.Counts.Stalled++
		h.Counts.ReportedStalls++
		if state == "partial" {
			first.State, first.Reason = "missing", "loop_age_missing"
			first.AgeSeconds, first.AgeObservedAt = nil, nil
			h.Counts.Stalled--
			h.Counts.Missing++
			h.Incomplete = true
		}
	}
	return h
}

func TestLoopHistoryStallsGapsRecoveryAndOwnedViews(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i, state := range []string{"no_reported_stall", "no_reported_stall", "stalled", "unknown", "partial", "no_reported_stall", "stalled", "no_reported_stall"} {
		at := now.Add(time.Duration(i) * time.Second)
		current := buildHistoryCurrent(t, at, "matching")
		if state != "unknown" {
			current.LoopHealth = deploymentLoopFixture(*current.Evidence, state)
			if !operatorobs.RuntimeLoopHealthValid(*current.Evidence, current.LoopHealth) {
				t.Fatal("invalid loop fixture")
			}
		}
		out = r.observe(current, at)
	}
	p := out.Progress.LoopHealth
	if p.StallRegressions != 2 || !p.LastRecoveryAt.Equal(now.Add(7*time.Second)) || !p.FirstStallAt.Equal(now.Add(2*time.Second)) || !p.LastStallAt.Equal(now.Add(6*time.Second)) || p.NoReportedStallThroughoutObservedPolls || !p.ObservedIncompleteEvidence || !out.Progress.MatchingThroughoutObservedPolls || out.History.TotalEvents != 7 {
		t.Fatalf("loop history lost evidence: %+v history=%+v", p, out.History)
	}
	out.History.Events[0].LoopHealth.Counts.Expected = 999
	*p.FirstStallAt = now.Add(-time.Hour)
	next := buildHistoryCurrent(t, now.Add(8*time.Second), "matching")
	next.LoopHealth = deploymentLoopFixture(*next.Evidence, "no_reported_stall")
	next.LoopHealth.Items[0].AgeSeconds = obsDeploymentEvidencePtr(newFloat(8))
	newer := r.observe(next, now.Add(8*time.Second))
	if newer.History.TotalEvents != 7 || newer.History.Events[0].LoopHealth.Counts.Expected == 999 || !newer.Progress.LoopHealth.FirstStallAt.Equal(now.Add(2*time.Second)) {
		t.Fatal("history aliases caller or records metric-clock churn")
	}
	var human bytes.Buffer
	writeObsDeploymentRuntimeBuilds(&human, newer)
	if !strings.Contains(human.String(), "loop_progress stall_regressions=2") || !strings.Contains(human.String(), "loop_health advisory=true state=no_reported_stall") {
		t.Fatal(human.String())
	}
	body, _ := json.Marshal(newer.History)
	if strings.Contains(string(body), "age_seconds") || strings.Contains(string(body), "target_id") {
		t.Fatal("aggregate loop history retained target observations")
	}
}

func newFloat(value float64) *float64 { return &value }

func TestLoopHealthIsAdvisoryAndMalformedJSONIsIsolated(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-90 * time.Second)
	release := strings.Repeat("a", 40)
	for _, fault := range []string{"stalled", "partial", "old_api", "wrong_type", "bad_loop"} {
		t.Run(fault, func(t *testing.T) {
			base := deploymentRuntimeBuildTest(t, now, after, release)
			deploymentReadinessFixture(&base, "ready", "ready")
			loops := deploymentLoopFixture(base, "stalled")
			if fault == "partial" {
				loops = deploymentLoopFixture(base, "partial")
			}
			value := operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, LoopHealth: loops}
			encoded, _ := json.Marshal(value)
			var raw map[string]any
			if err := json.Unmarshal(encoded, &raw); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "old_api":
				delete(raw, "loop_health")
			case "wrong_type":
				raw["loop_health"].(map[string]any)["items"].([]any)[0].(map[string]any)["reported_stalled"] = "private-provider-error"
			case "bad_loop":
				raw["loop_health"].(map[string]any)["items"].([]any)[0].(map[string]any)["loop"] = "private-provider-error"
			}
			serving := deploymentSnapshotTest(now)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer private-loop-token" {
					t.Error("lost protected read")
				}
				switch r.URL.Path {
				case operatorobs.RuntimeBuildsPath:
					_ = json.NewEncoder(w).Encode(raw)
				case operatorobs.ServingHealthPath:
					_ = json.NewEncoder(w).Encode(serving.serving.value)
				case operatorobs.NotificationHealthPath:
					_ = json.NewEncoder(w).Encode(serving.notifications)
				default:
					t.Error("unexpected read")
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_APID_URL", server.URL)
			opts := obsDeploymentOptions{release: release, after: after, expectedRole: "single-box-v1", wait: time.Minute, poll: time.Second}
			fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "private-loop-token"}, opts)
			report, code := runObsDeploymentVerification(t.Context(), opts, fetch, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("loop health added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
			if code != 0 || report.State != "passed" || report.HadConfirmedFaults || report.RuntimeBuilds.State != "matching" || report.RuntimeBuilds.Evidence.Readiness.State != "ready" {
				t.Fatalf("loop metadata changed qualification: %d %+v", code, report)
			}
			want := fault == "stalled" || fault == "partial"
			if (report.RuntimeBuilds.LoopHealth != nil) != want {
				t.Fatalf("loop metadata not isolated: %+v", report.RuntimeBuilds)
			}
			saved, _ := json.Marshal(report)
			if strings.Contains(string(saved), "private-provider-error") || strings.Contains(string(saved), "private-loop-token") {
				t.Fatal("raw loop data leaked")
			}
		})
	}
}

func TestLoopHistoryAndDetailShareReportBounds(t *testing.T) {
	now := time.Now().UTC()
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * time.Second)
		current := buildHistoryCurrent(t, at, "matching")
		state := "stalled"
		if i%2 == 0 {
			state = "no_reported_stall"
		}
		current.LoopHealth = deploymentLoopFixture(*current.Evidence, state)
		out = r.observe(current, at)
	}
	if out.History.TotalEvents != 80 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax || out.History.RetainedEvents > api.ObsPostDeployBuildEventsMax || !out.History.Truncated || out.Progress.LoopHealth.StallRegressions != 40 {
		t.Fatal("loop history exceeded retention or lost progress")
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: out}, len(body)/2)
	if fitted.RuntimeBuilds.LoopEnvelopeOmittedItems == 0 || fitted.RuntimeBuilds.Progress.LoopHealth.StallRegressions != 40 || out.LoopEnvelopeOmittedItems != 0 || len(out.LoopHealth.Items) != 6 {
		t.Fatal("report fit lost summaries or mutated saved data")
	}
}
