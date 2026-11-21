package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func deploymentRuntimeBuildTest(t *testing.T, now, after time.Time, release string) api.ObsRuntimeBuilds {
	t.Helper()
	return deploymentRuntimeBuildProfileTest(t, now, after, release, "single-box-v1")
}

func deploymentRuntimeBuildProfileTest(t *testing.T, now, after time.Time, release, role string) api.ObsRuntimeBuilds {
	t.Helper()
	selected, err := operatorobs.RuntimeBuildsSelection("release="+release+"&after="+after.Format(time.RFC3339Nano)+"&expected_role="+role, false, now)
	if err != nil {
		t.Fatal(err)
	}
	out := operatorobs.RuntimeBuilds(t.Context(), nil, nil, selected, now)
	out.Readiness = nil // Deliberately model an older API without optional readiness.
	out.Process = nil
	out.State, out.Reason = "matching", ""
	out.Counts = api.ObsRuntimeBuildCounts{Expected: len(out.Jobs), Observed: len(out.Jobs), Matching: len(out.Jobs), DistinctRevisions: 1}
	out.Items = []api.ObsRuntimeBuildItem{}
	for i := range out.Jobs {
		out.Jobs[i].State, out.Jobs[i].Reason = "available", ""
		out.Jobs[i].Expected = 1
		out.Items = append(out.Items, api.ObsRuntimeBuildItem{Job: out.Jobs[i].Job, Daemon: out.Jobs[i].Daemon,
			State: "matching", TargetID: strings.Repeat("1", 64), GitSHA: release, ObservedAt: &now, ScrapeObservedAt: &now})
	}
	if !operatorobs.RuntimeBuildsValid(out) {
		t.Fatal("invalid runtime build test fixture")
	}
	return out
}

func TestDeploymentRuntimeBuildsAdvisoryQualificationAndOwnership(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-90 * time.Second), wait: time.Minute, poll: time.Second, expectedRole: "single-box-v1"}
	for _, tc := range []string{"matching", "mixed", "older_server", "rate_limited", "invalid", "stale"} {
		t.Run(tc, func(t *testing.T) {
			snapshot := deploymentSnapshotTest(now)
			builds := deploymentRuntimeBuildTest(t, now, opts.after, opts.release)
			snapshot.runtimeBuilds = &builds
			want := "matching"
			switch tc {
			case "mixed":
				builds.State, builds.Reason = "mixed", "multiple_observed_revisions"
				builds.Counts.Matching, builds.Counts.Mismatched, builds.Counts.DistinctRevisions = 3, 1, 2
				builds.Items[0].State, builds.Items[0].Reason, builds.Items[0].GitSHA = "mismatched", "requested_revision_mismatch", strings.Repeat("b", 40)
				want = "mixed"
			case "older_server":
				snapshot.runtimeBuildErr = obsFetchHTTPFailure("runtime-builds", 404)
				want = "unknown"
			case "rate_limited":
				snapshot.runtimeBuildErr = obsFetchHTTPFailure("runtime-builds", http.StatusTooManyRequests)
				want = "unknown"
			case "invalid":
				builds.Items[0].Node = "private://credentials"
				want = "unknown"
			case "stale":
				builds.GeneratedAt = now.Add(-3 * time.Minute)
				want = "unknown"
			}
			report, code := runObsDeploymentVerification(t.Context(), opts, func(context.Context) obsDeploymentSnapshot { return snapshot }, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("advisory evidence added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
			if code != 0 || report.State != "passed" || report.HadConfirmedFaults || len(report.Checks) != 3 || report.RuntimeBuilds.State != want {
				t.Fatalf("advisory changed qualification: code=%d %+v", code, report)
			}
			if tc == "mixed" {
				builds.Items[0].GitSHA = strings.Repeat("c", 40)
				if report.RuntimeBuilds.Evidence.Items[0].GitSHA != strings.Repeat("b", 40) {
					t.Fatal("saved evidence aliases input")
				}
			}
			body, _ := json.Marshal(report)
			if strings.Contains(string(body), "private://") {
				t.Fatal("invalid source identity leaked")
			}
		})
	}
}

func TestCmdDeploymentRuntimeBuildsProtectedReadAndReport(t *testing.T) {
	for _, mode := range []string{"mixed", "older_server", "busy", "rate_limited"} {
		t.Run(mode, func(t *testing.T) {
			var want429Reason string
			switch mode {
			case "busy":
				want429Reason = "source_runtime_busy"
			case "rate_limited":
				want429Reason = "source_rate_limited"
			}
			stdout, stderr := obsBundleTestStreams(t)
			now := time.Now().UTC()
			after := now.Add(-90 * time.Second).Truncate(time.Second)
			release := strings.Repeat("a", 40)
			builds := deploymentRuntimeBuildTest(t, now, after, release)
			builds.State, builds.Reason = "mixed", "multiple_observed_revisions"
			builds.Counts.Matching, builds.Counts.Mismatched, builds.Counts.DistinctRevisions = 3, 1, 2
			builds.Items[0].State, builds.Items[0].Reason, builds.Items[0].GitSHA = "mismatched", "requested_revision_mismatch", strings.Repeat("b", 40)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer private-runtime-token" {
					t.Error("lost protected read")
				}
				snapshot := deploymentSnapshotTest(time.Now().UTC())
				switch r.URL.Path {
				case operatorobs.RuntimeBuildsPath:
					q := r.URL.Query()
					wantQuerySize := 3
					if want429Reason != "" {
						wantQuerySize++
						if q.Get("diagnostics") != "none" {
							t.Error("429 response retried without the diagnostic selection")
						}
					}
					if len(q) != wantQuerySize || q.Get("expected_role") != "single-box-v1" || q.Get("release") != release || q.Get("after") != after.Format(time.RFC3339Nano) {
						t.Error("unexpected build selection")
					}
					switch mode {
					case "older_server":
						w.WriteHeader(404)
						_, _ = w.Write([]byte("private-provider-response"))
						return
					case "busy":
						api.WriteProblem(w, api.ErrObsRuntimeBusy(api.ObsRuntimeBuildCollectionsMax))
						return
					case "rate_limited":
						api.WriteProblem(w, api.ErrAPIRateLimited(10, 1))
						return
					}
					_ = json.NewEncoder(w).Encode(builds)
				case operatorobs.ServingHealthPath:
					_ = json.NewEncoder(w).Encode(snapshot.serving.value)
				case operatorobs.NotificationHealthPath:
					_ = json.NewEncoder(w).Encode(snapshot.notifications)
				default:
					t.Error("unexpected read", r.URL.Path)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_APID_URL", srv.URL)
			path := filepath.Join(t.TempDir(), "report.json")
			args := []string{"--release", release, "--after", after.Format(time.RFC3339), "--expected-role", "single-box-v1", "--admin-token", "private-runtime-token", "--report", path}
			if want429Reason != "" {
				args = append(args, "--runtime-diagnostics", "none")
			}
			code := cmdObsVerifyDeployment(args)
			if code != 0 || calls.Load() != 3 {
				t.Fatalf("exit=%d reads=%d stdout=%s stderr=%s", code, calls.Load(), stdout, stderr)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(path)
			var report obsDeploymentReport
			if json.Unmarshal(body, &report) != nil || report.State != "passed" || report.RuntimeBuilds == nil || !report.RuntimeBuilds.Advisory || info.Mode().Perm() != 0600 || len(body) > api.ObsIncidentBundleResponseMaxBytes {
				t.Fatalf("invalid private report: %s", body)
			}
			if strings.Contains(string(body), "private-runtime-token") || strings.Contains(string(body), "private-provider-response") {
				t.Fatal("source secret retained")
			}
			if want429Reason != "" {
				runtimeBuilds := report.RuntimeBuilds
				if runtimeBuilds.State != "unknown" || runtimeBuilds.Reason != want429Reason || runtimeBuilds.Evidence != nil || runtimeBuilds.SourceHTTPStatus == nil || *runtimeBuilds.SourceHTTPStatus != http.StatusTooManyRequests || report.HadConfirmedFaults || len(report.Checks) != 3 {
					t.Fatalf("429 changed advisory semantics: %+v", runtimeBuilds)
				}
				if h := runtimeBuilds.History; h == nil || len(h.Events) != 1 || h.Events[0].Reason != want429Reason || h.Events[0].SourceHTTPStatus == nil || *h.Events[0].SourceHTTPStatus != http.StatusTooManyRequests {
					t.Fatalf("429 reason lost from poll history: %+v", runtimeBuilds.History)
				}
				if !strings.Contains(stdout.String(), "reason="+want429Reason+" source_http_status=429") ||
					strings.Contains(stdout.String(), "Check for overlapping verifiers") != (mode == "busy") {
					t.Fatalf("wrong terminal diagnostic for %s: %s", mode, stdout)
				}
			}
			if !strings.Contains(stdout.String(), "runtime_builds advisory=true") || mode == "mixed" && !strings.Contains(stdout.String(), "distinct_revisions=2") || mode == "older_server" && report.RuntimeBuilds.SourceHTTPStatus == nil {
				t.Fatal("human/JSON evidence missing", stdout.String())
			}
		})
	}
}

func TestDeploymentRuntimeBuildPreviewYieldsToReportEnvelope(t *testing.T) {
	now := time.Now().UTC()
	after := now.Add(-time.Minute)
	builds := deploymentRuntimeBuildTest(t, now, after, strings.Repeat("a", 40))
	report := obsDeploymentReport{RuntimeBuilds: &obsDeploymentRuntimeBuilds{Advisory: true, State: builds.State, Evidence: &builds}}
	empty := report
	copyBuilds := builds
	copyBuilds.Items = []api.ObsRuntimeBuildItem{}
	copyBuilds.OmittedItems = len(builds.Items)
	empty.RuntimeBuilds = &obsDeploymentRuntimeBuilds{Advisory: true, State: builds.State, Evidence: &copyBuilds, EnvelopeOmittedItems: 4}
	body, _ := json.MarshalIndent(empty, "", "  ")
	fitted := fitObsDeploymentReportWithin(report, len(body)+1)
	got, _ := json.MarshalIndent(fitted, "", "  ")
	if len(got) > len(body) || len(fitted.RuntimeBuilds.Evidence.Items) != 0 || fitted.RuntimeBuilds.EnvelopeOmittedItems != 4 || len(report.RuntimeBuilds.Evidence.Items) != 4 || fitted.RuntimeBuilds.Evidence.Counts != builds.Counts {
		t.Fatal("envelope dropped summary or mutated source")
	}
}

func TestDeploymentRuntimeBuildsCombinedMaximumReport(t *testing.T) {
	report, _ := deploymentEnvelopeFaultFixture(t)
	now := time.Now().UTC()
	report.QualificationEvidence.History = maximumQualificationHistory(now, report.QualificationEvidence.Checks)
	builds := deploymentRuntimeBuildTest(t, now, now.Add(-time.Minute), strings.Repeat("a", 40))
	for len(builds.Items) < api.ObsRuntimeBuildPreviewMax {
		builds.Items = append(builds.Items, builds.Items[0])
	}
	for i := range builds.Items {
		builds.Items[i].Node = strings.Repeat("n", api.ObsServingProbeNodeMaxBytes)
		builds.Items[i].Version = strings.Repeat("v", api.ObsRuntimeBuildVersionMaxBytes)
	}
	builds.Counts.Observed, builds.Counts.Matching = len(builds.Items), len(builds.Items)
	recorder := newObsDeploymentBuildRecorder()
	for i := range 41 {
		current := builds
		current.Counts.Observed, current.Counts.Matching, current.OmittedItems = len(builds.Items)+i%2, len(builds.Items)+i%2, i%2
		report.RuntimeBuilds = recorder.observe(&obsDeploymentRuntimeBuilds{Advisory: true, State: current.State, Evidence: &current}, now.Add(time.Duration(i)*time.Second))
	}
	before, _ := json.MarshalIndent(report, "", "  ")
	fitted := fitObsDeploymentReport(report)
	body, err := json.MarshalIndent(fitted, "", "  ")
	if err != nil || len(body)+1 > api.ObsIncidentBundleResponseMaxBytes || fitted.RuntimeBuilds.Evidence.Counts != builds.Counts || fitted.QualificationEvidence.History.TotalEvents != report.QualificationEvidence.History.TotalEvents || fitted.State != report.State || len(fitted.Checks) != len(report.Checks) {
		t.Fatalf("combined report overflow or qualification change: %d %v", len(body), err)
	}
	if fitted.RuntimeBuilds.Progress.Observations != 41 || fitted.RuntimeBuilds.History.TotalEvents != 41 || fitted.RuntimeBuilds.Progress.FirstMatchingAt == nil {
		t.Fatal("combined fitting discarded build progress")
	}
	after, _ := json.MarshalIndent(report, "", "  ")
	if !bytes.Equal(before, after) {
		t.Fatal("combined fitting mutated the saved report")
	}
}
