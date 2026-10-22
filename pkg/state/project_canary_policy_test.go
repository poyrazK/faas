package state

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func canaryFixture(t *testing.T, workers bool) ProjectCanaryPolicyState {
	t.Helper()
	s, err := NewProjectCanaryPolicy("rollout", "baseline", "candidate", workers, time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func canaryObserve(s ProjectCanaryPolicyState, http, worker string, baseline bool) ProjectCanaryObservation {
	return ProjectCanaryObservation{Generation: s.Generation, PreviousCheckedAt: s.CheckedAt, HTTPVerdict: http, WorkerVerdict: worker, BaselineHealthy: baseline}
}

func canaryRound(t *testing.T, s ProjectCanaryPolicyState, http, worker string, baseline bool, now time.Time) (ProjectCanaryPolicyState, ProjectCanaryEffect) {
	t.Helper()
	o := canaryObserve(s, http, worker, baseline)
	o.EvidenceStartedAt = s.StageStartedAt
	if s.CheckedAt != nil {
		o.EvidenceStartedAt = *s.CheckedAt
	}
	o.EvidenceEndedAt, o.BaselineCheckedAt = now, now
	next, effect, err := DecideProjectCanary(s, o, now)
	if err != nil {
		t.Fatal(err)
	}
	return next, effect
}

func TestProjectCanaryRejectsReusedWindowsAndStaleBaseline(t *testing.T) {
	s := canaryFixture(t, true)
	start := s.StageStartedAt
	s, _ = canaryRound(t, s, "healthy", "healthy", true, start.Add(time.Minute))
	o := canaryObserve(s, "healthy", "healthy", true)
	o.EvidenceStartedAt = start // Includes already counted deliveries.
	o.EvidenceEndedAt = start.Add(2 * time.Minute)
	o.BaselineCheckedAt = o.EvidenceEndedAt
	got, effect, err := DecideProjectCanary(s, o, o.EvidenceEndedAt)
	if err != nil || effect != ProjectCanaryNoEffect || !reflect.DeepEqual(got, s) {
		t.Fatal("overlapping evidence counted")
	}
	o.EvidenceStartedAt = *s.CheckedAt
	o.BaselineCheckedAt = start
	got, effect, err = DecideProjectCanary(s, o, o.EvidenceEndedAt)
	if err != nil || effect != ProjectCanaryNoEffect || got.HealthyRounds != 0 {
		t.Fatal("stale baseline allowed promotion")
	}
	o.BaselineCheckedAt = s.Deadline.Add(time.Second)
	_, effect, err = DecideProjectCanary(s, o, s.Deadline)
	if err != nil || effect != ProjectCanaryNeedsOperator {
		t.Fatal("future baseline authorized restoration")
	}
	o.BaselineCheckedAt = start
	_, effect, err = DecideProjectCanary(s, o, s.Deadline)
	if err != nil || effect != ProjectCanaryNeedsOperator {
		t.Fatal("stale baseline authorized restoration")
	}
}

func TestProjectCanaryProgressionAndRestart(t *testing.T) {
	s := canaryFixture(t, true)
	for _, step := range []struct {
		percent int
		effect  ProjectCanaryEffect
	}{{25, ProjectCanaryPromote}, {100, ProjectCanaryActivate}, {100, ProjectCanaryComplete}} {
		start := s.StageStartedAt
		original := s
		next, effect := canaryRound(t, s, "healthy", "healthy", true, start)
		if !reflect.DeepEqual(next, original) || effect != ProjectCanaryNoEffect {
			t.Fatal("promoted without a fresh stage interval")
		}
		s, effect = canaryRound(t, s, "healthy", "healthy", true, start.Add(time.Minute))
		if effect != ProjectCanaryNoEffect || s.HealthyRounds != 1 {
			t.Fatal("first observation did not require confirmation")
		}
		// Simulate the controller restarting from a serialized checkpoint, not
		// process-local counters. Monotonic clock data is absent after recovery.
		checkpoint, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var recovered ProjectCanaryPolicyState
		if err := json.Unmarshal(checkpoint, &recovered); err != nil {
			t.Fatal(err)
		}
		s, effect = canaryRound(t, recovered, "healthy", "healthy", true, start.Add(2*time.Minute))
		if s.Percent != step.percent || effect != step.effect {
			t.Fatalf("percent=%d effect=%s, want %d/%s", s.Percent, effect, step.percent, step.effect)
		}
		if effect != ProjectCanaryComplete && (s.CheckedAt != nil || s.HealthyRounds != 0) {
			t.Fatal("previous stage evidence leaked into next stage")
		}
	}
	if s.Status != "complete" {
		t.Fatal("full traffic was not separately verified")
	}
}

func TestProjectCanaryStaleRoundsAndClock(t *testing.T) {
	s := canaryFixture(t, false)
	stale := canaryObserve(s, "healthy", "", true)
	s, _ = canaryRound(t, s, "healthy", "", true, s.StageStartedAt.Add(time.Minute))
	for _, now := range []time.Time{s.StageStartedAt.Add(2 * time.Minute), s.Deadline.Add(time.Minute)} {
		got, effect, err := DecideProjectCanary(s, stale, now)
		if err != nil || effect != ProjectCanaryNoEffect || !reflect.DeepEqual(got, s) {
			t.Fatal("stale controller modified checkpoint")
		}
	}
	for _, now := range []time.Time{s.StageStartedAt.Add(30 * time.Second), s.StageStartedAt.Add(90 * time.Second)} {
		got, effect := canaryRound(t, s, "healthy", "", true, now)
		if effect != ProjectCanaryNoEffect || !reflect.DeepEqual(got, s) {
			t.Fatal("backward clock or short interval counted")
		}
	}
	// A round from the prior stage cannot be counted at the next stage even
	// when both stages have a nil initial timestamp.
	initial := canaryFixture(t, false)
	s, _ = canaryRound(t, s, "healthy", "", true, s.StageStartedAt.Add(2*time.Minute))
	got, _, err := DecideProjectCanary(s, canaryObserve(initial, "healthy", "", true), s.StageStartedAt.Add(time.Minute))
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatal("old stage observation counted")
	}
}

func TestProjectCanaryWorkerEvidenceAndRollback(t *testing.T) {
	for _, tc := range []struct {
		name, http, worker string
		baseline           bool
		want               string
	}{
		{"low_traffic", "healthy", "insufficient_evidence", true, "running"},
		{"worker_failure", "healthy", "regressed", true, "aborted"},
		{"http_failure", "regressed", "healthy", true, "aborted"},
		{"bad_baseline", "regressed", "regressed", false, "running"},
		{"unknown_http", "unknown", "regressed", true, "running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := canaryFixture(t, true)
			start := s.StageStartedAt
			s, _ = canaryRound(t, s, tc.http, tc.worker, tc.baseline, start.Add(time.Minute))
			s, effect := canaryRound(t, s, tc.http, tc.worker, tc.baseline, start.Add(2*time.Minute))
			if s.Status != tc.want {
				t.Fatalf("status=%s want=%s", s.Status, tc.want)
			}
			if tc.want == "aborted" && effect != ProjectCanaryAbort {
				t.Fatal("pre-activation regression attempted graph restoration")
			}
		})
	}
	for _, percent := range []int{5, 25, 100} {
		for _, healthy := range []bool{true, false} {
			s := canaryFixture(t, true)
			s.Percent = percent
			s, effect := canaryRound(t, s, "unknown", "insufficient_evidence", healthy, s.Deadline)
			want := ProjectCanaryAbort
			if percent == 100 {
				want = ProjectCanaryRestore
			}
			if !healthy {
				want = ProjectCanaryNeedsOperator
			}
			if effect != want {
				t.Fatalf("percent=%d baseline=%t effect=%s want=%s", percent, healthy, effect, want)
			}
			if _, err := SelectProjectCanaryRelease(s, "new-work", s.Deadline); err == nil {
				t.Fatal("terminal rollout admitted new work")
			}
		}
	}
}

func TestProjectCanaryUnknownResetsConfirmation(t *testing.T) {
	s := canaryFixture(t, true)
	start := s.StageStartedAt
	for i, verdict := range []string{"healthy", "insufficient_evidence", "healthy"} {
		s, _ = canaryRound(t, s, "healthy", verdict, true, start.Add(time.Duration(i+1)*time.Minute))
	}
	if s.Percent != 5 || s.HealthyRounds != 1 {
		t.Fatal("non-consecutive evidence promoted")
	}
}

func TestProjectCanaryCohortsAreStableAndMonotonic(t *testing.T) {
	s := canaryFixture(t, false)
	counts := map[int]int{}
	for i := range api.ProjectCanaryBucketCount {
		key := fmt.Sprintf("job-root-%d", i)
		selected := false
		for _, percent := range []int{5, 25, 100} {
			s.Percent = percent
			a, err := SelectProjectCanaryRelease(s, key, s.StageStartedAt)
			if err != nil {
				t.Fatal(err)
			}
			b, err := SelectProjectCanaryRelease(s, key, s.StageStartedAt.Add(time.Minute))
			if err != nil || a != b {
				t.Fatal("same cohort key changed within stage")
			}
			candidate := a == s.CandidateReleaseID
			if selected && !candidate {
				t.Fatal("promotion removed a candidate cohort")
			}
			selected = candidate
			if candidate {
				counts[percent]++
			}
		}
	}
	if counts[5] < 400 || counts[5] > 600 || counts[25] < 2300 || counts[25] > 2700 || counts[100] != api.ProjectCanaryBucketCount {
		t.Fatalf("unexpected cohort distribution: %v", counts)
	}
}

func TestProjectCanaryRejectsInvalidAndExpiredCheckpoints(t *testing.T) {
	for _, mutate := range []func(*ProjectCanaryPolicyState){
		func(s *ProjectCanaryPolicyState) { s.ID = "" },
		func(s *ProjectCanaryPolicyState) { s.CandidateReleaseID = s.BaselineReleaseID },
		func(s *ProjectCanaryPolicyState) { s.Percent = 50 },
		func(s *ProjectCanaryPolicyState) { s.Generation = 0 },
		func(s *ProjectCanaryPolicyState) { s.Status = "complete" },
		func(s *ProjectCanaryPolicyState) { s.HealthyRounds = 1 },
		func(s *ProjectCanaryPolicyState) { s.Deadline = s.Deadline.Add(time.Hour) },
	} {
		s := canaryFixture(t, false)
		mutate(&s)
		if _, err := SelectProjectCanaryRelease(s, "job", s.StageStartedAt); err == nil {
			t.Fatal("invalid checkpoint accepted for routing")
		}
		if _, _, err := DecideProjectCanary(s, canaryObserve(s, "healthy", "", true), s.StageStartedAt.Add(time.Minute)); err == nil {
			t.Fatal("invalid checkpoint accepted for promotion")
		}
	}
	s := canaryFixture(t, false)
	if _, err := SelectProjectCanaryRelease(s, "job", s.Deadline); err == nil {
		t.Fatal("expired checkpoint admitted work")
	}
	if _, err := SelectProjectCanaryRelease(s, "", s.StageStartedAt); err == nil {
		t.Fatal("empty identity accepted")
	}
}
