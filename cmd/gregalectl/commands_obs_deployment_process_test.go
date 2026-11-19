package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func deploymentProcessFixture(e *api.ObsRuntimeBuilds, at *time.Time, state string) {
	n := len(e.Items)
	c := api.ObsRuntimeProcessCounts{Fresh: n}
	p := &api.ObsRuntimeProcess{State: "baseline", Reason: "awaiting_comparison", Counts: c, ComparisonAt: obsDeploymentEvidencePtr(at)}
	for i := range e.Items {
		value := float64(130)
		item := &api.ObsRuntimeProcessItem{State: "fresh", UptimeSeconds: &value, ObservedAt: obsDeploymentEvidencePtr(&e.GeneratedAt), Comparison: "baseline"}
		if at != nil {
			old := float64(100)
			item.Comparison = "unchanged"
			item.PreviousObservedAt = obsDeploymentEvidencePtr(at)
			item.PreviousUptimeSeconds = &old
		}
		e.Items[i].Process = item
	}
	if at != nil {
		p.State, p.Reason = "unchanged", ""
		p.Counts.Compared = n
		p.Counts.Unchanged = n
		first := e.Items[0].Process
		switch state {
		case "changed":
			value := float64(5)
			first.UptimeSeconds = &value
			first.Comparison, first.RevisionRelation = "changed", "same"
			p.State, p.Reason = "changed", "observed_process_change"
			p.Counts.Changed, p.Counts.SameRevisionChanges = 1, 1
			p.Counts.Unchanged--
		case "new_target":
			first.Comparison = "new_target"
			first.PreviousObservedAt, first.PreviousUptimeSeconds = nil, nil
			p.State, p.Reason, p.Incomplete = "incomplete", "incomplete_continuity_evidence", true
			p.Counts.NewTargets = 1
			p.Counts.Compared--
			p.Counts.Unchanged--
		}
	}
	e.Process = p
}

func TestProcessHistoryCountsIntervalsWithoutClockChurn(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i, state := range []string{"baseline", "unchanged", "unchanged", "changed", "changed", "unknown", "unchanged"} {
		at := now.Add(time.Duration(i) * 30 * time.Second)
		current := buildHistoryCurrent(t, at, "matching")
		if state == "unknown" {
			current = &obsDeploymentRuntimeBuilds{Advisory: true, State: "unknown", Reason: "source_server_error"}
		} else {
			var prior *time.Time
			if i > 0 {
				p := at.Add(-30 * time.Second)
				if i == 6 {
					p = at.Add(-60 * time.Second)
				}
				prior = &p
			}
			deploymentProcessFixture(current.Evidence, prior, state)
			if !operatorobs.RuntimeProcessValid(*current.Evidence) {
				t.Fatalf("invalid process fixture: %+v", current.Evidence.Process)
			}
		}
		out = r.observe(current, at)
	}
	p := out.Progress.Process
	if p.ObservedChanges != 2 || p.SameRevisionChanges != 2 || p.PollsWithComparison != 5 || p.ComparedPairs != 20 || !p.ObservedIncompleteEvidence || !p.FirstChangeAt.Equal(now.Add(90*time.Second)) || !p.LastChangeAt.Equal(now.Add(120*time.Second)) || out.History.TotalEvents != 6 {
		t.Fatalf("process history lost intervals: %+v history=%+v", p, out.History)
	}
	// Saved views own their clocks and aggregate data.
	out.History.Events[2].Process.Counts.Changed = 999
	*out.History.Events[2].Process.ComparisonAt = now.Add(-time.Hour)
	*out.Progress.Process.FirstChangeAt = now.Add(-time.Hour)
	current := buildHistoryCurrent(t, now.Add(210*time.Second), "matching")
	prior := now.Add(180 * time.Second)
	deploymentProcessFixture(current.Evidence, &prior, "unchanged")
	next := r.observe(current, now.Add(210*time.Second))
	if next.History.TotalEvents != 6 || next.History.Events[2].Process.Counts.Changed != 1 || !next.Progress.Process.FirstChangeAt.Equal(now.Add(90*time.Second)) {
		t.Fatal("process snapshot aliases recorder or counts clock churn")
	}
	var human bytes.Buffer
	writeObsDeploymentRuntimeBuilds(&human, next)
	if !strings.Contains(human.String(), "observed_changes=2") || !strings.Contains(human.String(), "runtime_process advisory=true state=unchanged") {
		t.Fatal(human.String())
	}
	body, _ := json.Marshal(next.History)
	if strings.Contains(string(body), "uptime_seconds") || strings.Contains(string(body), "target_id") {
		t.Fatal("aggregate process history retained individual target data")
	}
}

func TestProcessHistoryRepeatedResponseAndRetention(t *testing.T) {
	now := time.Now().UTC()
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * 30 * time.Second)
		prior := at.Add(-30 * time.Second)
		current := buildHistoryCurrent(t, at, "matching")
		deploymentProcessFixture(current.Evidence, &prior, "changed")
		out = r.observe(current, at)
	}
	if out.History.TotalEvents != 80 || out.History.RetainedEvents > api.ObsPostDeployBuildEventsMax || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax || !out.History.Truncated || out.Progress.Process.ObservedChanges != 80 {
		t.Fatalf("unbounded process history: %+v", out.History)
	}
	replay := r.observe(out, *out.Progress.LastObservedAt)
	if replay.Progress.Process.ObservedChanges != 80 || !replay.Progress.Process.ObservedIncompleteEvidence || replay.History.TotalEvents != 80 {
		t.Fatal("replayed interval became another process change")
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: out}, len(body)/2)
	if fitted.RuntimeBuilds.History.EnvelopeOmittedEvents == 0 || fitted.RuntimeBuilds.Progress.Process.ObservedChanges != 80 || out.History.EnvelopeOmittedEvents != 0 {
		t.Fatal("envelope fitting discarded process totals or mutated history")
	}
}

func TestProcessEvidenceDoesNotQualifyDeployment(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-90 * time.Second), wait: time.Minute, poll: time.Second, expectedRole: "single-box-v2"}
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed", true: "invalid"}[invalid], func(t *testing.T) {
			value := deploymentRuntimeBuildProfileTest(t, now, opts.after, opts.release, opts.expectedRole)
			prior := now.Add(-30 * time.Second)
			deploymentProcessFixture(&value, &prior, "changed")
			if invalid {
				value.Items[0].Process.ComparisonReason = "private-process-error"
			}
			snapshot := deploymentSnapshotTest(now)
			snapshot.runtimeBuilds = &value
			snapshot.runtimeProcessCompareAt = prior
			report, code := runObsDeploymentVerification(t.Context(), opts, func(context.Context) obsDeploymentSnapshot { return snapshot }, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("process evidence added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
			if code != 0 || report.State != "passed" || report.HadConfirmedFaults || report.RuntimeBuilds.State != "matching" {
				t.Fatalf("process evidence changed health: %d %+v", code, report)
			}
			if invalid != (report.RuntimeBuilds.Evidence.Process == nil) {
				t.Fatal("optional process sanitization failed")
			}
			body, _ := json.Marshal(report)
			if strings.Contains(string(body), "private-process-error") {
				t.Fatal("invalid process detail leaked")
			}
		})
	}
}

func TestProcessFetcherBindsComparisonAndSupportsOlderAPI(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-90 * time.Second)
	release := strings.Repeat("a", 40)
	first := deploymentRuntimeBuildTest(t, now.Add(-40*time.Second), after, release)
	deploymentProcessFixture(&first, nil, "baseline")
	second := deploymentRuntimeBuildTest(t, now.Add(-10*time.Second), after, release)
	prior := first.GeneratedAt
	deploymentProcessFixture(&second, &prior, "changed")
	legacy := deploymentRuntimeBuildTest(t, now, after, release)
	serving := deploymentSnapshotTest(now)
	var buildCalls, total atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		total.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer private-process-token" {
			t.Error("lost protected read")
		}
		switch r.URL.Path {
		case operatorobs.RuntimeBuildsPath:
			i := buildCalls.Add(1)
			switch i {
			case 1:
				if r.URL.Query().Has("compare_at") {
					t.Error("initial observation compared without a baseline")
				}
				_ = json.NewEncoder(w).Encode(first)
			case 2:
				if r.URL.Query().Get("compare_at") != prior.Format(time.RFC3339Nano) {
					t.Error("comparison not bound to prior response")
				}
				_ = json.NewEncoder(w).Encode(second)
			case 3:
				if r.URL.Query().Get("compare_at") != second.GeneratedAt.Format(time.RFC3339Nano) {
					t.Error("overlapping comparison interval")
				}
				http.Error(w, "private-process-error", 400)
			case 4:
				if r.URL.Query().Has("compare_at") {
					t.Error("older API could not recover original read")
				}
				_ = json.NewEncoder(w).Encode(legacy)
			default:
				t.Error("extra process poll")
			}
		case operatorobs.ServingHealthPath:
			_ = json.NewEncoder(w).Encode(serving.serving.value)
		case operatorobs.NotificationHealthPath:
			_ = json.NewEncoder(w).Encode(serving.notifications)
		default:
			t.Error("unexpected source")
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_APID_URL", server.URL)
	opts := obsDeploymentOptions{release: release, after: after, expectedRole: "single-box-v1"}
	fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "private-process-token"}, opts)
	for i := range 4 {
		snapshot := fetch(t.Context())
		out := assessObsDeploymentRuntimeBuilds(snapshot, opts, now)
		if i == 2 {
			if out.State != "unknown" {
				t.Fatal("older API error was hidden")
			}
			continue
		}
		if out.State != "matching" || i == 1 && (out.Evidence.Process == nil || out.Evidence.Process.Counts.Changed != 1) || i == 3 && out.Evidence.Process != nil {
			t.Fatalf("process transport lost compatible identity: %+v", out)
		}
	}
	if total.Load() != 12 || buildCalls.Load() != 4 {
		t.Fatal("process evidence added reads or retries")
	}
}

func TestProcessFetcherAllowsBoundedServerClockSkew(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-time.Minute)
	release := strings.Repeat("a", 40)
	first := deploymentRuntimeBuildTest(t, now.Add(10*time.Second), after, release)
	deploymentProcessFixture(&first, nil, "baseline")
	second := deploymentRuntimeBuildTest(t, now.Add(11*time.Second), after, release)
	prior := first.GeneratedAt
	deploymentProcessFixture(&second, &prior, "unchanged")
	snapshot := deploymentSnapshotTest(now)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case operatorobs.RuntimeBuildsPath:
			if calls.Add(1) == 1 {
				_ = json.NewEncoder(w).Encode(first)
			} else {
				if r.URL.Query().Get("compare_at") != prior.Format(time.RFC3339Nano) {
					t.Error("bounded server clock skew discarded baseline")
				}
				_ = json.NewEncoder(w).Encode(second)
			}
		case operatorobs.ServingHealthPath:
			_ = json.NewEncoder(w).Encode(snapshot.serving.value)
		case operatorobs.NotificationHealthPath:
			_ = json.NewEncoder(w).Encode(snapshot.notifications)
		default:
			t.Error("unexpected read")
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_APID_URL", server.URL)
	opts := obsDeploymentOptions{release: release, after: after, expectedRole: "single-box-v1"}
	fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "fixture"}, opts)
	_ = fetch(t.Context())
	observed := fetch(t.Context())
	out := assessObsDeploymentRuntimeBuilds(observed, opts, now)
	if out.Evidence == nil || out.Evidence.Process == nil || out.Evidence.Process.Counts.Compared != 4 {
		t.Fatalf("clock skew lost process comparison: %+v", out)
	}
}

func TestProcessMalformedOptionalJSONKeepsOtherEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-90 * time.Second)
	release := strings.Repeat("a", 40)
	for _, fault := range []string{"summary_type", "count_type", "sample_type"} {
		t.Run(fault, func(t *testing.T) {
			value := deploymentRuntimeBuildTest(t, now, after, release)
			deploymentReadinessFixture(&value, "ready", "ready")
			deploymentProcessFixture(&value, nil, "baseline")
			body, _ := json.Marshal(value)
			var raw map[string]any
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "summary_type":
				raw["process"] = "private-process-error"
			case "count_type":
				raw["process"].(map[string]any)["counts"].(map[string]any)["fresh"] = "private-process-error"
			case "sample_type":
				raw["items"].([]any)[0].(map[string]any)["process"].(map[string]any)["uptime_seconds"] = "private-process-error"
			}
			serving := deploymentSnapshotTest(now)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			opts := obsDeploymentOptions{release: release, after: after, expectedRole: "single-box-v1"}
			fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "fixture"}, opts)
			out := assessObsDeploymentRuntimeBuilds(fetch(t.Context()), opts, now)
			if out.State != "matching" || out.Evidence == nil || out.Evidence.Readiness == nil || out.Evidence.Readiness.State != "ready" || out.Evidence.Process != nil {
				t.Fatalf("optional JSON error poisoned other evidence: %+v", out)
			}
			saved, _ := json.Marshal(out)
			if strings.Contains(string(saved), "private-process-error") {
				t.Fatal("raw optional JSON leaked")
			}
		})
	}
}
