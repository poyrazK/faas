package state

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectCanaryControlMemory(t *testing.T) {
	testProjectCanaryControl(t, func(t *testing.T) Store { return NewMemStore() })
}

func testProjectCanaryControl(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"early_abort", "full_abort", "expired_baseline", "replay_expired", "recover", "stale_generation", "operator_config", "concurrent", "worker_pin"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCanaryStoreFixtureWithWorker(t, factory(t), scenario == "worker_pin")
			ctx := t.Context()
			control := f.store.(ProjectCanaryControlStore)
			canary := f.store.(ProjectCanaryStore)
			if scenario == "full_abort" || scenario == "recover" || scenario == "expired_baseline" || scenario == "replay_expired" {
				for range 4 {
					f.advance(t, false)
				}
			}
			p, err := canary.ProjectCanaryCheckpoint(ctx, f.account, f.attempt)
			if err != nil {
				t.Fatal(err)
			}
			req := api.ProjectReleaseControlRequest{RequestID: strings.Repeat("a", 32), ExpectedGeneration: p.Generation}
			if _, err = control.ControlProjectCanary(ctx, uuid.NewString(), f.attempt, "abort", req); !errors.Is(err, ErrNotFound) {
				t.Fatalf("cross-account control: %v", err)
			}
			if scenario == "expired_baseline" {
				expireRetainedCanaryBaseline(t, f)
				if _, err := control.ControlProjectCanary(ctx, f.account, f.attempt, "abort", req); !errors.Is(err, ErrProjectReleaseBaselineExpired) {
					t.Fatalf("expired baseline accepted: %v", err)
				}
				after, err := canary.ProjectCanaryCheckpoint(ctx, f.account, f.attempt)
				if err != nil || after.Generation != p.Generation || after.Control != nil {
					t.Fatal("rejected expiry changed durable decision")
				}
				return
			}
			if scenario == "stale_generation" {
				req.ExpectedGeneration++
				if _, err = control.ControlProjectCanary(ctx, f.account, f.attempt, "abort", req); !errors.Is(err, ErrConflict) {
					t.Fatalf("stale control accepted: %v", err)
				}
				return
			}
			if scenario == "operator_config" {
				_, err = f.store.CreateProjectEnvironmentConfigVersion(ctx, ProjectEnvironmentConfig{AccountID: f.account, ProjectID: f.project, EnvironmentSlug: "production", Values: f.previous.Values, ConfigHash: f.previous.ConfigHash})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = control.ControlProjectCanary(ctx, f.account, f.attempt, "abort", req); !errors.Is(err, ErrConflict) {
					t.Fatalf("operator ABA overwritten: %v", err)
				}
				return
			}
			if scenario == "concurrent" {
				var wg sync.WaitGroup
				results := make(chan error, 2)
				for _, id := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32)} {
					wg.Go(func() {
						_, err := control.ControlProjectCanary(ctx, f.account, f.attempt, "abort", api.ProjectReleaseControlRequest{RequestID: id, ExpectedGeneration: p.Generation})
						results <- err
					})
				}
				wg.Wait()
				close(results)
				won := 0
				for err := range results {
					if err == nil {
						won++
					} else if !errors.Is(err, ErrConflict) {
						t.Fatal(err)
					}
				}
				if won != 1 {
					t.Fatalf("concurrent decisions committed %d times", won)
				}
				return
			}
			var pinned Invocation
			var version InvocationVersion
			if scenario == "worker_pin" {
				for range 1000 {
					pinned, err = f.store.EnqueueInvocation(ctx, Invocation{AccountID: f.account, AppID: f.apps[1].ID, Source: InvocationQueue, Method: "POST", Path: "/", QueueName: "orders", QueueBindingID: f.progress(t).Worker.BindingID, DeploymentScope: "production", Payload: json.RawMessage(`{}`), DueAt: time.Now()})
					if err != nil {
						t.Fatal(err)
					}
					selected, err := SelectProjectCanaryRelease(p, pinned.ID, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					if selected != p.CandidateReleaseID {
						if err = f.store.CancelInvocation(ctx, pinned.ID); err != nil {
							t.Fatal(err)
						}
						pinned = Invocation{}
						continue
					}
					pinned, err = f.store.ClaimInvocation(ctx, pinned.ID, "", 60)
					if err != nil {
						t.Fatal(err)
					}
					_, version, err = ResolveInvocationVersion(ctx, f.store, pinned)
					if err != nil {
						t.Fatal(err)
					}
					break
				}
				if pinned.ID == "" || version.ReleaseID != p.CandidateReleaseID {
					t.Fatal("candidate job cohort was not exercised")
				}
			}
			safe, err := control.ControlProjectCanary(ctx, f.account, f.attempt, "abort", req)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "replay_expired" {
				expireRetainedCanaryBaseline(t, f)
			}
			if _, err = control.ControlProjectCanary(ctx, f.account, f.attempt, "abort", req); err != nil {
				t.Fatalf("lost-response retry: %v", err)
			}
			if scenario == "replay_expired" {
				return
			}
			if scenario == "worker_pin" {
				if err = f.store.FailInvocation(ctx, pinned.ID, "retry", time.Nanosecond, 5, WithClaimAttempt(pinned.Attempts)); err != nil {
					t.Fatal(err)
				}
				pinned, err = f.store.ClaimInvocation(ctx, pinned.ID, "", 60)
				if err != nil {
					t.Fatal(err)
				}
				_, retry, err := ResolveInvocationVersion(ctx, f.store, pinned)
				if err != nil || retry != version {
					t.Fatalf("abort retargeted pinned work: %+v %v", retry, err)
				}
			}
			if p.Percent < 100 {
				if safe.Status != "rolled_back" || safe.RestoredReleaseID != f.baseline {
					t.Fatalf("early abort: %+v", safe)
				}
				id, _, err := canary.ResolveProjectCanary(ctx, f.apps[0].ID, "production", "new-work")
				if err != nil || id != f.baseline {
					t.Fatalf("new work did not stay on baseline: %s %v", id, err)
				}
			} else {
				if _, _, err := canary.ResolveProjectCanary(ctx, f.apps[0].ID, "production", "new-work"); !errors.Is(err, ErrConflict) {
					t.Fatalf("operator decision allowed unassigned work: %v", err)
				}
				stale := &ProjectReleaseVerificationRound{Generation: p.Generation, Evidence: []api.ProjectReleaseProbeEvidence{{Probe: safe.Verification.Probes[0], StableRequests: 10, CandidateRequests: 10}}}
				if _, err = f.store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, f.account, f.attempt, stale); err != nil {
					t.Fatal(err)
				}
				if f.progress(t).Status != "verifying" {
					t.Fatal("pre-decision probe authorized restore")
				}
				if scenario == "recover" {
					// Expire the bounded decision window without inventing healthy evidence.
					expireCanaryControl(t, f)
					if _, err = f.store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, f.account, f.attempt, nil); err != nil {
						t.Fatal(err)
					}
					if f.progress(t).Status != "rollback_failed" {
						t.Fatal("unknown baseline did not block recovery")
					}
					p, err = canary.ProjectCanaryCheckpoint(ctx, f.account, f.attempt)
					if err != nil {
						t.Fatal(err)
					}
					req = api.ProjectReleaseControlRequest{RequestID: strings.Repeat("b", 32), ExpectedGeneration: p.Generation}
					if _, err = control.ControlProjectCanary(ctx, f.account, f.attempt, "recover", req); err != nil {
						t.Fatal(err)
					}
				}
				round := f.seedRound(t)
				round.Evidence[0].StableErrors = 1
				if _, err = f.store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, f.account, f.attempt, &round); err != nil {
					t.Fatal(err)
				}
				if f.progress(t).Status != "verifying" {
					t.Fatal("unhealthy baseline authorized restore")
				}
				safe = f.advance(t, false)
				if safe.Status != "rolled_back" || safe.Verification.Control.Status != "restored" {
					t.Fatalf("healthy recovery failed: %+v", safe)
				}
				if _, err = control.ControlProjectCanary(ctx, f.account, f.attempt, safe.Verification.Control.Action, req); err != nil {
					t.Fatal(err)
				}
			}
			head, err := f.store.ProjectEnvironmentConfigLatest(ctx, f.account, f.project, "production")
			want := f.previous.Version
			if p.Percent == 100 {
				want += 2
			}
			if err != nil || head.Version != want || head.ConfigHash != f.previous.ConfigHash {
				t.Fatalf("graph/config restore not paired: %+v %v", head, err)
			}
		})
	}
}

func expireCanaryControl(t *testing.T, f canaryStoreFixture) {
	t.Helper()
	p, err := f.store.(ProjectCanaryStore).ProjectCanaryCheckpoint(t.Context(), f.account, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	p.StageStartedAt = time.Now().UTC().Add(-2 * api.ProjectReleaseVerificationTimeout)
	p.Deadline = p.StageStartedAt.Add(api.ProjectReleaseVerificationTimeout)
	p.CheckedAt = nil
	p.HealthyRounds, p.RegressedRounds = 0, 0
	safe := f.progress(t)
	syncCanaryVerification(safe, p)
	raw, _ := json.Marshal(p)
	safeRaw, _ := json.Marshal(safe)
	switch s := f.store.(type) {
	case *MemStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		key := f.account + "\x00" + f.attempt
		c := s.projectCanaries[key]
		c.Policy = p
		s.projectCanaries[key] = c
		entry := s.projectSubmissions[key]
		var receipt api.ProjectSubmissionReceipt
		if err = json.Unmarshal(entry.Payload, &receipt); err != nil {
			t.Fatal(err)
		}
		receipt.Apply.SafeRelease = safe
		entry.Payload, _ = json.Marshal(receipt)
		s.projectSubmissions[key] = entry
	case *PgStore:
		if _, err = s.pool.Exec(t.Context(), "UPDATE project_release_canaries SET policy=$3::jsonb WHERE account_id=$1 AND attempt_id=$2", f.account, f.attempt, raw); err != nil {
			t.Fatal(err)
		}
		if _, err = s.pool.Exec(t.Context(), "UPDATE project_submissions SET payload=jsonb_set(payload,'{apply,safe_release}',$3::jsonb) WHERE account_id=$1 AND attempt_id=$2", f.account, f.attempt, safeRaw); err != nil {
			t.Fatal(err)
		}
	}
}

func expireRetainedCanaryBaseline(t *testing.T, f canaryStoreFixture) {
	t.Helper()
	switch store := f.store.(type) {
	case *MemStore:
		store.mu.Lock()
		defer store.mu.Unlock()
		base := store.projectReleaseSets[f.baseline]
		expired := time.Now().UTC().Add(-time.Second)
		base.ExpiresAt = &expired
		store.projectReleaseSets[f.baseline] = base
	case *PgStore:
		if _, err := store.pool.Exec(t.Context(), "UPDATE project_release_sets SET expires_at=now()-interval '1 second' WHERE id=$1", f.baseline); err != nil {
			t.Fatal(err)
		}
	}
}
