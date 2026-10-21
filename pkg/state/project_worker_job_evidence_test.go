package state

import (
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestProjectWorkerJobVerdict(t *testing.T) {
	healthy := api.ProjectWorkerJobSample{Attempts: 10, DurationMS: 1000, QueueDelayMS: 1000}
	cases := []struct {
		name                string
		candidate, baseline api.ProjectWorkerJobSample
		want                string
	}{
		{"healthy", healthy, healthy, "healthy"},
		{"no_traffic", api.ProjectWorkerJobSample{}, healthy, "insufficient_evidence"},
		{"no_baseline", healthy, api.ProjectWorkerJobSample{}, "insufficient_evidence"},
		{"failed_jobs", api.ProjectWorkerJobSample{Attempts: 10, Failures: 4}, healthy, "regressed"},
		{"dead_letter", api.ProjectWorkerJobSample{Attempts: 5, Failures: 5}, healthy, "regressed"},
		{"unfinished", api.ProjectWorkerJobSample{Attempts: 10, Incomplete: 1}, healthy, "insufficient_evidence"},
		{"bad_baseline", healthy, api.ProjectWorkerJobSample{Attempts: 10, Failures: 3}, "insufficient_evidence"},
		{"retry_regression", api.ProjectWorkerJobSample{Attempts: 10, Retries: 5}, healthy, "regressed"},
		{"slow_processing", api.ProjectWorkerJobSample{Attempts: 10, DurationMS: 30000}, healthy, "regressed"},
		{"slow_queue", api.ProjectWorkerJobSample{Attempts: 10, QueueDelayMS: 30000}, healthy, "regressed"},
		{"tolerated_errors", api.ProjectWorkerJobSample{Attempts: 20, Failures: 2}, healthy, "healthy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProjectWorkerJobVerdict(tc.candidate, tc.baseline); got != tc.want {
				t.Fatalf("verdict=%s want=%s", got, tc.want)
			}
		})
	}
}
func TestProjectWorkerJobRoundsRequireNewEvidence(t *testing.T) {
	now := time.Now().UTC()
	deadline := now.Add(8 * time.Minute)
	probe := api.ProjectReleaseProbe{AppID: "http", Method: "GET", Path: "/health"}
	health := &api.ProjectWorkerHealth{Status: "insufficient_evidence"}
	safe := &api.ProjectSafeRelease{Status: "verifying", Verification: &api.ProjectReleaseVerification{Deadline: &deadline, Probes: []api.ProjectReleaseProbe{probe}, WorkerHealth: health}}
	evidence := []api.ProjectReleaseProbeEvidence{{Probe: probe, CandidateRequests: 10, StableRequests: 10}}
	advanceVerification(safe, &ProjectReleaseVerificationRound{Evidence: evidence}, now)
	if safe.Verification.HealthyRounds != 0 || safe.Status != "verifying" {
		t.Fatal("HTTP success masked missing worker evidence")
	}
	health.Status = "regressed"
	previous := *safe.Verification.CheckedAt
	advanceVerification(safe, &ProjectReleaseVerificationRound{PreviousCheckedAt: &previous, Evidence: evidence}, now.Add(time.Minute))
	if safe.Status != "verifying" || safe.Verification.RegressedRounds != 1 {
		t.Fatal("first worker regression did not count")
	}
	previous = *safe.Verification.CheckedAt
	if advanceVerification(safe, &ProjectReleaseVerificationRound{PreviousCheckedAt: &previous, Evidence: evidence}, now.Add(time.Minute)) {
		t.Fatal("same round counted twice")
	}
	advanceVerification(safe, &ProjectReleaseVerificationRound{PreviousCheckedAt: &previous, Evidence: evidence}, now.Add(2*time.Minute))
	if safe.Status != "rolling_back" {
		t.Fatal("worker regressions did not trigger rollback with healthy HTTP")
	}
}

func TestProjectWorkerJobBacklogVerdict(t *testing.T) {
	sample := api.ProjectWorkerJobSample{Attempts: 10, DurationMS: 100}
	health := &api.ProjectWorkerHealth{Candidate: sample, Baseline: sample, PendingJobs: 10, OldestPendingAgeMS: 30000}
	if projectWorkerHealthVerdict(health) != "regressed" {
		t.Fatal("growing delayed backlog accepted")
	}
	health.OldestPendingAgeMS = 100
	if projectWorkerHealthVerdict(health) != "healthy" {
		t.Fatal("fresh producer burst incorrectly regressed")
	}
	health.Candidate.Attempts = 0
	if projectWorkerHealthVerdict(health) != "insufficient_evidence" {
		t.Fatal("backlog without delivery evidence proved health")
	}
}
func TestProjectWorkerJobRegressionCannotRestoreFailingHTTPBaseline(t *testing.T) {
	now := time.Now()
	deadline := now.Add(time.Minute)
	probe := api.ProjectReleaseProbe{AppID: "http", Method: "GET", Path: "/health"}
	safe := &api.ProjectSafeRelease{Status: "verifying", Verification: &api.ProjectReleaseVerification{Deadline: &deadline, Probes: []api.ProjectReleaseProbe{probe}, WorkerHealth: &api.ProjectWorkerHealth{Status: "regressed"}}}
	evidence := []api.ProjectReleaseProbeEvidence{{Probe: probe, CandidateRequests: 10, StableRequests: 10, StableErrors: 1}}
	advanceVerification(safe, &ProjectReleaseVerificationRound{Evidence: evidence}, now)
	if safe.Verification.RegressedRounds != 0 {
		t.Fatal("known failing HTTP baseline accepted as rollback target")
	}
}

func TestProjectWorkerJobFailingBaselineBlocksHTTPRollback(t *testing.T) {
	now := time.Now()
	deadline := now.Add(time.Minute)
	probe := api.ProjectReleaseProbe{AppID: "http", Method: "GET", Path: "/health"}
	safe := &api.ProjectSafeRelease{Status: "verifying", Verification: &api.ProjectReleaseVerification{Deadline: &deadline, Probes: []api.ProjectReleaseProbe{probe}, WorkerHealth: &api.ProjectWorkerHealth{Status: "insufficient_evidence", Baseline: api.ProjectWorkerJobSample{Attempts: 5, Failures: 5}}}}
	evidence := []api.ProjectReleaseProbeEvidence{{Probe: probe, CandidateRequests: 10, CandidateErrors: 10, StableRequests: 10}}
	advanceVerification(safe, &ProjectReleaseVerificationRound{Evidence: evidence}, now)
	if safe.Verification.RegressedRounds != 0 {
		t.Fatal("HTTP regression selected known failing worker baseline")
	}
	advanceVerification(safe, nil, deadline.Add(time.Second))
	if safe.Status != "rollback_failed" {
		t.Fatal("deadline restored known failing worker baseline")
	}
}
