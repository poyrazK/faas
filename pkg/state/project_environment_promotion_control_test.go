package state

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionControlMemory(t *testing.T) {
	testPromotionControlStore(t, func(*testing.T) Store { return NewMemStore() })
}

func newPromotionControlFixture(t *testing.T, store Store) *promotionSnapshotFixture {
	t.Helper()
	f := newPromotionSnapshotFixture(t, store, false)
	policy := &api.ProjectPromotionHealthPolicy{}
	for _, app := range f.apps {
		policy.Probes = append(policy.Probes, api.ProjectReleaseProbe{AppID: app.ID, Slug: app.Slug, Method: "GET", Path: "/health"})
	}
	sort.Slice(policy.Probes, func(i, j int) bool { return policy.Probes[i].AppID < policy.Probes[j].AppID })
	f.promotion.HealthPolicy = policy
	f.create(t)
	f.prepare(t)
	return f
}

func promotionControlRound(t *testing.T, f *promotionSnapshotFixture, stableErrors int) *api.ProjectPromotionHealth {
	t.Helper()
	hs := f.store.(ProjectPromotionHealthStore)
	h, err := hs.ProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID)
	requirePromotionSnapshot(t, err)
	if h.CheckedAt != nil {
		at := time.Now().Add(-api.ProjectReleaseVerificationInterval - time.Second)
		h.CheckedAt = &at
		setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
	}
	h, err = hs.AdvanceProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: h.Generation, Evidence: promotionHealthEvidence(h.Policy, 1, stableErrors)})
	requirePromotionSnapshot(t, err)
	return h
}

func testPromotionControlStore(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"early_abort", "race", "restore", "blocked_retry", "baseline_unhealthy", "new_intent", "config_drift", "old_controller"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPromotionControlFixture(t, factory(t))
			ctx := t.Context()
			hs, cs := f.store.(ProjectPromotionHealthStore), f.store.(ProjectPromotionControlStore)
			publisher := f.store.(ProjectEnvironmentPromotionReleaseSetStore)
			h, err := hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			if scenario != "early_abort" && scenario != "race" {
				h.Phase, h.Verdict, h.HealthyRounds = "cutover", "healthy", api.ProjectReleaseVerificationRounds
				at := time.Now()
				h.CheckedAt = &at
				h.Evidence = promotionHealthEvidence(h.Policy, 0, 0)
				setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
				_, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
				requirePromotionSnapshot(t, err)
				h, err = hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
			}
			req := api.ProjectPromotionControlRequest{RequestID: strings.Repeat("a", 32), ExpectedGeneration: h.Generation}
			stale := req
			stale.ExpectedGeneration++
			if _, err = cs.RequestProjectPromotionControl(ctx, f.account.ID, f.promotion.ID, "abort", stale); !errors.Is(err, ErrConflict) {
				t.Fatal("stale decision admitted", err)
			}
			if _, err = cs.RequestProjectPromotionControl(ctx, "00000000-0000-0000-0000-000000000001", f.promotion.ID, "abort", req); !errors.Is(err, ErrNotFound) {
				t.Fatal("foreign decision admitted", err)
			}
			if scenario == "race" {
				var wg sync.WaitGroup
				results := make(chan error, 2)
				for _, id := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32)} {
					wg.Add(1)
					go func() {
						defer wg.Done()
						r := req
						r.RequestID = id
						_, e := cs.RequestProjectPromotionControl(ctx, f.account.ID, f.promotion.ID, "abort", r)
						results <- e
					}()
				}
				wg.Wait()
				close(results)
				accepted, rejected := 0, 0
				for e := range results {
					if e == nil {
						accepted++
					} else if errors.Is(e, ErrConflict) {
						rejected++
					} else {
						t.Fatal(e)
					}
				}
				if accepted != 1 || rejected != 1 {
					t.Fatal("decisions did not serialize", accepted, rejected)
				}
				return
			}
			receipt, err := cs.RequestProjectPromotionControl(ctx, f.account.ID, f.promotion.ID, "abort", req)
			requirePromotionSnapshot(t, err)
			if receipt.Request != req || receipt.OperatorAccountID != f.account.ID {
				t.Fatal("lost operator identity", receipt)
			}
			again, err := cs.RequestProjectPromotionControl(ctx, f.account.ID, f.promotion.ID, "abort", req)
			requirePromotionSnapshot(t, err)
			if !reflect.DeepEqual(receipt, again) {
				t.Fatal("replay changed acknowledgement")
			}
			if _, err = cs.RequestProjectPromotionControl(ctx, f.account.ID, f.promotion.ID, "recover", req); !errors.Is(err, ErrConflict) {
				t.Fatal("request identity reused", err)
			}
			if _, err = hs.AdvanceProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: h.Generation, Action: "complete"}); !errors.Is(err, ErrConflict) {
				t.Fatal("in-flight observation survived abort", err)
			}
			if _, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates); scenario == "early_abort" && !errors.Is(err, ErrConflict) {
				t.Fatal("aborted candidate cut over", err)
			}
			if scenario == "early_abort" {
				if receipt.Status != "aborted" {
					t.Fatal(receipt)
				}
				active, err := f.store.(ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production")
				requirePromotionSnapshot(t, err)
				if active.ID != f.targetGraph.ID {
					t.Fatal("early abort changed destination")
				}
				for _, candidate := range f.candidates {
					d, e := f.store.DeploymentByID(ctx, candidate.DeploymentID)
					requirePromotionSnapshot(t, e)
					if d.Status != DeploySuperseded {
						t.Fatal("unused candidate not retired", d.Status)
					}
				}
				return
			}
			if receipt.Status != "pending" {
				t.Fatal(receipt)
			}
			if _, err = publisher.RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.targetGraph.Members); !errors.Is(err, ErrConflict) {
				t.Fatal("unqualified restoration published", err)
			}
			if scenario == "old_controller" {
				if s, ok := f.store.(*PgStore); ok {
					if _, err = s.pool.Exec(ctx, "UPDATE project_environment_promotions SET restored_target_release_set_id=previous_target_release_set_id WHERE id=$1", f.promotion.ID); err == nil {
						t.Fatal("old publisher bypassed restoration guard")
					}
					if _, err = s.pool.Exec(ctx, "UPDATE project_environment_promotion_health SET payload=payload-'control' WHERE promotion_id=$1", f.promotion.ID); err == nil {
						t.Fatal("old controller erased decision")
					}
					if _, err = s.pool.Exec(ctx, "UPDATE project_environment_promotion_controls SET payload=jsonb_set(payload,'{action}','\"recover\"') WHERE promotion_id=$1", f.promotion.ID); err == nil {
						t.Fatal("decision identity mutable")
					}
				}
				return
			}
			if scenario == "blocked_retry" {
				current, e := hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, e)
				current, e = hs.AdvanceProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: current.Generation, Action: "rollback_failed"})
				requirePromotionSnapshot(t, e)
				if current.Control.Status != "blocked" {
					t.Fatal("failure not durable")
				}
				nextReq := api.ProjectPromotionControlRequest{RequestID: strings.Repeat("b", 32), ExpectedGeneration: current.Generation}
				_, err = cs.RequestProjectPromotionControl(ctx, f.account.ID, f.promotion.ID, "recover", nextReq)
				requirePromotionSnapshot(t, err)
				old, e := cs.RequestProjectPromotionControl(ctx, f.account.ID, f.promotion.ID, "abort", req)
				requirePromotionSnapshot(t, e)
				if old.Status != "blocked" || old.Request != req {
					t.Fatal("old decision replaced by recovery")
				}
			}
			if scenario == "new_intent" {
				_, err = f.store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production", 1800, f.candidates)
				requirePromotionSnapshot(t, err)
				current := promotionControlRound(t, f, 0)
				if current.Phase != "conflicted" || current.Control.Status != "blocked" {
					t.Fatal("new graph overwritten", current)
				}
				return
			}
			current := promotionControlRound(t, f, 1)
			if current.HealthyRounds != 0 || current.Phase != "restoring" {
				t.Fatal("unhealthy baseline qualified", current)
			}
			if scenario == "baseline_unhealthy" {
				return
			}
			current = promotionControlRound(t, f, 0)
			current = promotionControlRound(t, f, 0)
			if current.Phase != "rolling_back" {
				t.Fatal("fresh baseline did not qualify", current)
			}
			if _, e := f.store.UpdateProjectEnvironmentPromotionReleaseSets(ctx, f.account.ID, f.promotion.ID, "", f.targetGraph.ID); !errors.Is(e, ErrConflict) {
				t.Fatal("unguarded publisher bypassed qualified restoration", e)
			}
			if scenario == "config_drift" {
				requirePromotionSnapshot(t, f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.apps[0].ID, "production", "MODE", "new-intent"))
			}
			restored, e := publisher.RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.targetGraph.Members)
			if scenario == "config_drift" {
				if !errors.Is(e, ErrConflict) {
					t.Fatal("new config overwritten", e)
				}
				return
			}
			requirePromotionSnapshot(t, e)
			_, err = f.store.UpdateProjectEnvironmentPromotionRollback(ctx, f.account.ID, f.promotion.ID, "rolled_back", "", nil)
			requirePromotionSnapshot(t, err)
			current, err = hs.AdvanceProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: current.Generation, Action: "rolled_back"})
			requirePromotionSnapshot(t, err)
			if current.Control.Status != "restored" || current.Control.ResolvedAt == nil || current.Phase != "failed" {
				t.Fatal("restoration not durable", current)
			}
			active, e := f.store.(ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production")
			requirePromotionSnapshot(t, e)
			if active.ID != restored.ID || !reflect.DeepEqual(active.Members, cloneProjectReleaseSet(f.targetGraph).Members) {
				t.Fatal("wrong baseline restored")
			}
		})
	}
}

func TestPromotionControlDeadlineAndMigration(t *testing.T) {
	now := time.Now().UTC()
	h := &api.ProjectPromotionHealth{Generation: 1, Phase: "verifying", Policy: api.ProjectPromotionHealthPolicy{Probes: []api.ProjectReleaseProbe{{AppID: "api", Slug: "api", Method: "GET", Path: "/health"}}}}
	p := ProjectEnvironmentPromotion{Status: "running", AccountID: "account", TargetReleaseSetID: "candidate", PreviousTargetReleaseSetID: "baseline"}
	req := api.ProjectPromotionControlRequest{RequestID: strings.Repeat("a", 32), ExpectedGeneration: 1}
	next, err := requestPromotionControl(p, h, "candidate", "abort", req, now)
	requirePromotionSnapshot(t, err)
	expired, err := decidePromotionHealth(next, p, PromotionHealthObservation{Generation: next.Generation}, next.Control.Deadline)
	requirePromotionSnapshot(t, err)
	if expired.Phase != "rollback_failed" || expired.Control.Status != "blocked" {
		t.Fatal("deadline extended", expired)
	}
	h.Policy.Migration = &api.ProjectPromotionMigrationPolicy{}
	if _, err = requestPromotionControl(p, h, "candidate", "abort", req, now); !errors.Is(err, ErrConflict) {
		t.Fatal("migration bypassed recovery", err)
	}
}

func testPromotionControlWorker(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"restore", "early_abort", "baseline_not_ready", "expired_retention"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			f := newPromotionWorkerFixture(t, factory(t))
			cs, hs := f.store.(ProjectPromotionControlStore), f.store.(ProjectPromotionHealthStore)
			worker := f.apps[1]
			seedPromotionWorkerRuntime(t, f, f.candidates[1].DeploymentID)
			before := enqueuePromotionWorkerJob(t, f)
			_, _, err := ResolveInvocationVersion(ctx, f.store, before)
			requirePromotionSnapshot(t, err)
			SeedProjectWorkerJobSamplesForTest(t, f.store, worker.ID, f.account.ID, f.view.Worker.BindingID, f.previous[1].ID, time.Now().Add(-time.Second), false)
			promotionWorkerRound(t, f)
			h := promotionWorkerRound(t, f)
			if h.Phase != "cutover" {
				t.Fatal("worker not qualified", h)
			}
			if scenario != "early_abort" {
				_, err = f.store.(ProjectEnvironmentPromotionReleaseSetStore).PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
				requirePromotionSnapshot(t, err)
			}
			h, err = hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			_, err = cs.RequestProjectPromotionControl(ctx, f.account.ID, f.promotion.ID, "abort", api.ProjectPromotionControlRequest{RequestID: strings.Repeat("a", 32), ExpectedGeneration: h.Generation})
			requirePromotionSnapshot(t, err)
			held, err := f.store.(ProjectReleaseWorkerStore).HeldProjectReleaseWorkers(ctx, worker.ID)
			requirePromotionSnapshot(t, err)
			if scenario == "early_abort" {
				if len(held) != 0 {
					t.Fatal("aborted candidate hold survived", held)
				}
				return
			}
			if len(held) != 1 || held[0] != f.previous[1].ID {
				t.Fatal("restoration lost bounded baseline hold", held)
			}
			after := enqueuePromotionWorkerJob(t, f)
			_, selected, err := ResolveInvocationVersion(ctx, f.store, after)
			requirePromotionSnapshot(t, err)
			if selected.DeploymentID != f.candidates[1].DeploymentID {
				t.Fatal("pending restoration changed new-job routing")
			}
			h = promotionControlRound(t, f, 0)
			if h.HealthyRounds != 0 {
				t.Fatal("baseline startup proof bypassed", h)
			}
			if scenario == "baseline_not_ready" {
				return
			}
			seedPromotionWorkerRuntime(t, f, f.previous[1].ID)
			h = promotionControlRound(t, f, 0)
			h = promotionControlRound(t, f, 0)
			if h.Phase != "rolling_back" {
				t.Fatal("baseline worker failed to qualify", h)
			}
			if scenario == "expired_retention" {
				switch s := f.store.(type) {
				case *MemStore:
					s.mu.Lock()
					graph := s.projectReleaseSets[f.targetGraph.ID]
					at := time.Now().Add(-time.Second)
					graph.ExpiresAt = &at
					s.projectReleaseSets[graph.ID] = graph
					s.mu.Unlock()
				case *PgStore:
					_, err = s.pool.Exec(ctx, "UPDATE project_release_sets SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", f.targetGraph.ID)
					requirePromotionSnapshot(t, err)
				}
			}
			_, err = f.store.(ProjectEnvironmentPromotionReleaseSetStore).RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.targetGraph.Members)
			if scenario == "expired_retention" {
				if !errors.Is(err, ErrConflict) {
					t.Fatal("expired baseline restored", err)
				}
				return
			}
			requirePromotionSnapshot(t, err)
			_, selected, err = ResolveInvocationVersion(ctx, f.store, after)
			requirePromotionSnapshot(t, err)
			if selected.DeploymentID != f.candidates[1].DeploymentID {
				t.Fatal("rollback moved an admitted retry")
			}
			requirePromotionSnapshot(t, f.store.CompleteInvocation(ctx, before.ID, []byte(`{}`)))
			requirePromotionSnapshot(t, f.store.CompleteInvocation(ctx, after.ID, []byte(`{}`)))
			next := enqueuePromotionWorkerJob(t, f)
			_, selected, err = ResolveInvocationVersion(ctx, f.store, next)
			requirePromotionSnapshot(t, err)
			if selected.DeploymentID != f.previous[1].ID {
				t.Fatal("new job did not follow restored graph")
			}
		})
	}
}

func TestPromotionControlWorkerMemory(t *testing.T) {
	testPromotionControlWorker(t, func(*testing.T) Store { return NewMemStore() })
}
