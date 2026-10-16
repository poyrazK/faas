package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func enableStabilizationFixture(t *testing.T, f canaryStoreFixture) {
	t.Helper()
	safe := f.progress(t)
	safe.Verification.Stabilization = &api.ProjectReleaseStabilization{Enabled: true, Status: "waiting"}
	switch s := f.store.(type) {
	case *MemStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		key := f.account + "\x00" + f.attempt
		entry := s.projectSubmissions[key]
		var receipt api.ProjectSubmissionReceipt
		if err := json.Unmarshal(entry.Payload, &receipt); err != nil {
			t.Fatal(err)
		}
		receipt.Apply.SafeRelease = safe
		entry.Payload, _ = json.Marshal(receipt)
		s.projectSubmissions[key] = entry
	case *PgStore:
		raw, _ := json.Marshal(safe)
		if _, err := s.pool.Exec(t.Context(), "UPDATE project_submissions SET payload=jsonb_set(payload,'{apply,safe_release}',$3::jsonb) WHERE account_id=$1 AND attempt_id=$2", f.account, f.attempt, raw); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectStabilizationMemory(t *testing.T) {
	testProjectStabilization(t, func(t *testing.T) Store { return NewMemStore() })
}

func testProjectStabilization(t *testing.T, factory func(*testing.T) Store) {
	for _, outcome := range []string{"delayed_regression", "unknown_baseline", "completed", "newer_graph", "newer_config", "operator_abort"} {
		t.Run(outcome, func(t *testing.T) {
			f := newCanaryStoreFixture(t, factory(t))
			enableStabilizationFixture(t, f)
			for range 6 {
				f.advance(t, false)
			}
			safe := f.progress(t)
			if safe.Status != "succeeded" || !api.ProjectReleaseMonitoring(safe) {
				t.Fatalf("monitor not started after success: %+v", safe)
			}
			jobs, err := f.store.(ProjectSafeReleaseStore).ListPendingSafeProjectSubmissions(t.Context())
			if err != nil || len(jobs) != 1 {
				t.Fatalf("successful attempt lost monitor: %v %v", jobs, err)
			}
			if outcome == "newer_graph" {
				newer, err := f.store.(ProjectReleaseSetStore).PublishProjectReleaseSet(t.Context(), f.account, f.project, "production", 1800, f.old)
				if err != nil {
					t.Fatal(err)
				}
				safe = f.advance(t, true)
				if safe.Verification.Stabilization.Status != "conflicted" {
					t.Fatal("monitor did not relinquish ownership")
				}
				active, _ := f.store.(ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), f.account, f.project, "production")
				if active.ID != newer.ID {
					t.Fatal("monitor overwrote newer graph")
				}
				return
			}
			if outcome == "newer_config" {
				_, err := f.store.CreateProjectEnvironmentConfigVersion(t.Context(), ProjectEnvironmentConfig{AccountID: f.account, ProjectID: f.project, EnvironmentSlug: "production", Values: f.previous.Values, ConfigHash: f.previous.ConfigHash})
				if err != nil {
					t.Fatal(err)
				}
				safe = f.advance(t, true)
				if safe.Verification.Stabilization.Status != "conflicted" {
					t.Fatal("monitor ignored configuration ownership change")
				}
				id, _, err := f.store.(ProjectCanaryStore).ResolveProjectCanary(t.Context(), f.apps[0].ID, "production", "new-work")
				if err != nil || id != safe.ReleaseID {
					t.Fatalf("stopped monitor blocked current serving graph: %s %v", id, err)
				}
				return
			}
			if outcome == "operator_abort" {
				_, err := f.store.(ProjectCanaryControlStore).ControlProjectCanary(t.Context(), f.account, f.attempt, "abort", api.ProjectReleaseControlRequest{RequestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExpectedGeneration: safe.Verification.Generation})
				if err != nil {
					t.Fatal(err)
				}
				safe = f.advance(t, false)
				if safe.Status != "rolled_back" {
					t.Fatalf("monitor could not be aborted: %+v", safe)
				}
				return
			}
			if outcome == "unknown_baseline" {
				for range 2 {
					round := f.seedRound(t)
					round.Evidence[0].CandidateErrors = 1
					round.Evidence[0].StableErrors = 1
					_, err := f.store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round)
					if err != nil {
						t.Fatal(err)
					}
				}
				safe = f.progress(t)
				if safe.Status != "succeeded" || safe.Verification.Stabilization.Health != "unknown" {
					t.Fatal("unhealthy baseline authorized rollback")
				}
				return
			}
			if outcome == "completed" {
				safe = f.advance(t, false)
				// Expire test clocks without changing identity or production ownership.
				expireCanaryControl(t, f)
				_, err := f.store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, nil)
				if err != nil {
					t.Fatal(err)
				}
				safe = f.progress(t)
				if safe.Verification.Stabilization.Status != "completed" || safe.Status != "succeeded" {
					t.Fatalf("window did not finish: %+v", safe)
				}
				jobs, _ = f.store.(ProjectSafeReleaseStore).ListPendingSafeProjectSubmissions(t.Context())
				if len(jobs) != 0 {
					t.Fatal("finished monitoring keeps reconciling")
				}
				return
			}
			round := f.seedRound(t)
			round.Evidence[0].CandidateErrors = 1
			if _, err := f.store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round); err != nil {
				t.Fatal(err)
			}
			// A duplicate observation must never create the second confirmation.
			if _, err := f.store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round); err != nil {
				t.Fatal(err)
			}
			if f.progress(t).Status != "succeeded" {
				t.Fatal("duplicate regression rolled back")
			}
			safe = f.advance(t, true)
			if safe.Status != "rolled_back" || safe.Verification.Stabilization.Status != "rolled_back" {
				t.Fatalf("delayed failure missed: %+v", safe)
			}
			head, err := f.store.ProjectEnvironmentConfigLatest(t.Context(), f.account, f.project, "production")
			if err != nil || head.Version != f.previous.Version+2 || head.ConfigHash != f.previous.ConfigHash {
				t.Fatalf("monitor restore was not paired: %+v %v", head, err)
			}
		})
	}
}

func TestProjectStabilizationEvidenceRequiresWorkerAndFreshWindows(t *testing.T) {
	p, err := NewProjectCanaryPolicy("watch", "baseline", "candidate", true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p.Stabilizing, p.Status, p.Percent = true, "complete", 100
	o := canaryObserve(p, "healthy", "unknown", true)
	o.EvidenceStartedAt = p.StageStartedAt
	o.EvidenceEndedAt = p.StageStartedAt.Add(time.Minute)
	o.BaselineCheckedAt = o.EvidenceEndedAt
	next, _, err := decideProjectStabilization(p, o, o.EvidenceEndedAt)
	if err != nil || next.HealthyRounds != 0 {
		t.Fatalf("unknown jobs counted as healthy: %+v %v", next, err)
	}
	if repeated, _, err := decideProjectStabilization(next, o, o.EvidenceEndedAt); err != nil || repeated.Generation != next.Generation {
		t.Fatal("stale monitor evidence reused")
	}
}

func TestProjectStabilizationDetectsWorkerRetryStorm(t *testing.T) {
	p, err := NewProjectCanaryPolicy("watch", "baseline", "candidate", true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p.Stabilizing, p.Status, p.Percent = true, "complete", 100
	verdict := ProjectWorkerJobVerdict(api.ProjectWorkerJobSample{Attempts: 10, Retries: 5}, api.ProjectWorkerJobSample{Attempts: 10})
	for i := range 2 {
		start := p.StageStartedAt
		if p.CheckedAt != nil {
			start = *p.CheckedAt
		}
		end := start.Add(api.ProjectReleaseVerificationInterval)
		o := ProjectCanaryObservation{Generation: p.Generation, PreviousCheckedAt: p.CheckedAt, EvidenceStartedAt: start, EvidenceEndedAt: end, BaselineCheckedAt: end, HTTPVerdict: "healthy", WorkerVerdict: verdict, BaselineHealthy: true}
		var effect ProjectCanaryEffect
		p, effect, err = decideProjectStabilization(p, o, end)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && effect != ProjectCanaryNoEffect || i == 1 && effect != ProjectCanaryRestore {
			t.Fatalf("retry storm decision=%s", effect)
		}
	}
}
