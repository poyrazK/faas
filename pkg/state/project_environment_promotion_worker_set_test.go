package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261019120040
func TestPromotionWorkerSetMemory(t *testing.T) {
	testPromotionWorkerSet(t, func(*testing.T) Store { return NewMemStore() })
	t.Run("recovery", func(t *testing.T) {
		testPromotionRecoveryMode(t, func(*testing.T) Store { return NewMemStore() }, true, true)
	})
}

func newPromotionWorkerSetRawFixture(t *testing.T, store Store, migrate bool) *promotionSnapshotFixture {
	t.Helper()
	f := newPromotionSnapshotFixture(t, store, false, false, migrate, true, false, true)
	f.promotion.HealthPolicy = serviceGraphHealthPolicy(f)
	f.promotion.HealthPolicy.Workers = copyPromotionWorkers(f.view.Workers)
	f.promotion.HealthPolicy.Migration = f.view.Migration
	if f.view.Worker != nil || len(f.view.Workers) != 3 {
		t.Fatal("worker set was projected as a single worker")
	}
	return f
}
func newPromotionWorkerSetFixture(t *testing.T, store Store, migrate bool) *promotionSnapshotFixture {
	t.Helper()
	f := newPromotionWorkerSetRawFixture(t, store, migrate)
	f.create(t)
	f.prepare(t)
	if migrate {
		for _, app := range f.apps[1:] {
			held, err := store.(ProjectReleaseWorkerStore).HeldProjectReleaseWorkers(t.Context(), app.ID)
			requirePromotionSnapshot(t, err)
			if len(held) != 0 {
				t.Fatal("worker started before migration receipt")
			}
		}
		completeCoordinatedMigration(t, f)
	}
	return f
}
func promotionWorkerSetMember(f *promotionSnapshotFixture, index int) *promotionSnapshotFixture {
	g := *f
	g.apps = []App{f.apps[0], f.apps[index]}
	g.previous = []Deployment{f.previous[0], f.previous[index]}
	g.candidates = []ProjectReleaseMember{f.candidates[0], f.candidates[index]}
	view := *f.view
	for _, worker := range f.view.Workers {
		if worker.AppID == f.apps[index].ID {
			view.Worker = worker
		}
	}
	g.view = &view
	return &g
}
func seedPromotionWorkerSet(t *testing.T, f *promotionSnapshotFixture, omitStartup, omitBaseline int) {
	t.Helper()
	for i := 1; i < len(f.apps); i++ {
		member := promotionWorkerSetMember(f, i)
		if i != omitStartup {
			seedPromotionWorkerRuntime(t, member, f.candidates[i].DeploymentID)
		}
		if i != omitBaseline {
			SeedProjectWorkerJobSamplesForTest(t, f.store, f.apps[i].ID, f.account.ID, member.view.Worker.BindingID, f.previous[i].ID, time.Now().Add(-time.Second), false)
		}
	}
}
func seedPromotionWorkerSetCandidate(t *testing.T, f *promotionSnapshotFixture, failed, omit int, at ...time.Time) {
	t.Helper()
	sampleAt := time.Now().Add(-time.Second)
	if len(at) > 0 {
		sampleAt = at[0]
	}
	for i := 1; i < len(f.apps); i++ {
		if i == omit {
			continue
		}
		member := promotionWorkerSetMember(f, i)
		SeedProjectWorkerJobSamplesForTest(t, f.store, f.apps[i].ID, f.account.ID, member.view.Worker.BindingID, f.candidates[i].DeploymentID, sampleAt, i == failed)
	}
}
func testPromotionWorkerSet(t *testing.T, factory func(*testing.T) Store) {
	for _, migrate := range []bool{false, true} {
		t.Run(map[bool]string{false: "workers", true: "coordinated"}[migrate], func(t *testing.T) {
			for _, scenario := range []string{"success", "last_startup_missing", "last_baseline_missing", "last_health_missing", "consumer_drift", "last_regression", "rollback_startup", "pins", "immutable_policy"} {
				t.Run(scenario, func(t *testing.T) {
					ctx := t.Context()
					f := newPromotionWorkerSetFixture(t, factory(t), migrate)
					store := f.store
					omitted := 0
					if scenario == "last_startup_missing" {
						omitted = len(f.apps) - 1
					}
					omittedBaseline := 0
					if scenario == "last_baseline_missing" {
						omittedBaseline = len(f.apps) - 1
					}
					seedPromotionWorkerSet(t, f, omitted, omittedBaseline)
					promotionWorkerRound(t, f)
					h := promotionWorkerRound(t, f)
					if scenario == "last_startup_missing" || scenario == "last_baseline_missing" {
						if h.Phase != "preflight" || h.Verdict != "unknown" {
							t.Fatal("one missing worker startup or baseline allowed cutover", h)
						}
						return
					}
					if h.Phase != "cutover" || len(h.Workers) != 3 || h.Worker != nil {
						t.Fatal("complete worker set failed preflight", h)
					}
					if scenario == "immutable_policy" {
						h.Policy.Workers[0].BindingHash = "mutated"
						again, err := store.(ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
						requirePromotionSnapshot(t, err)
						if again.Policy.Workers[0].BindingHash == "mutated" {
							t.Fatal("policy alias escaped store")
						}
						if s, ok := store.(*PgStore); ok {
							if _, err = s.pool.Exec(ctx, "UPDATE project_environment_promotion_health SET payload=payload #- '{policy,workers}' WHERE promotion_id=$1", f.promotion.ID); err == nil {
								t.Fatal("older controller erased worker set")
							}
						}
						return
					}
					for i := 1; i < len(f.apps); i++ {
						held, err := store.(ProjectReleaseWorkerStore).HeldProjectReleaseWorkers(ctx, f.apps[i].ID)
						requirePromotionSnapshot(t, err)
						if len(held) != 1 || held[0] != f.candidates[i].DeploymentID {
							t.Fatal("one worker missing durable startup hold", held)
						}
					}
					if scenario == "consumer_drift" {
						last := promotionWorkerSetMember(f, len(f.apps)-1)
						limit := 3
						_, err := store.(QueueBindingConsumerStore).UpdateQueueBindingWithConsumer(ctx, f.account.ID, last.apps[1].ID, last.view.Worker.BindingID, UpdateQueueBindingParams{MaxConcurrency: &limit})
						requirePromotionSnapshot(t, err)
						h = promotionWorkerRound(t, f)
						if h.Phase != "conflicted" {
							t.Fatal("last consumer drift ignored", h)
						}
						return
					}
					var before []Invocation
					if scenario == "pins" {
						for i := 1; i < len(f.apps); i++ {
							inv := enqueuePromotionWorkerJob(t, promotionWorkerSetMember(f, i))
							_, pin, err := ResolveInvocationVersion(ctx, store, inv)
							requirePromotionSnapshot(t, err)
							if pin.DeploymentID != f.previous[i].ID {
								t.Fatal("baseline job did not pin its selected worker")
							}
							before = append(before, inv)
						}
					}
					_, err := store.(ProjectEnvironmentPromotionReleaseSetStore).PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
					requirePromotionSnapshot(t, err)
					if scenario == "pins" {
						for i, inv := range before {
							_, pin, err := ResolveInvocationVersion(ctx, store, inv)
							requirePromotionSnapshot(t, err)
							if pin.DeploymentID != f.previous[i+1].ID {
								t.Fatal("old job lost its worker version")
							}
							next := enqueuePromotionWorkerJob(t, promotionWorkerSetMember(f, i+1))
							_, pin, err = ResolveInvocationVersion(ctx, store, next)
							requirePromotionSnapshot(t, err)
							if pin.DeploymentID != f.candidates[i+1].DeploymentID {
								t.Fatal("new job did not switch with full graph")
							}
						}
						return
					}
					h, err = store.(ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
					requirePromotionSnapshot(t, err)
					cutover := time.Now().Add(-3 * time.Minute)
					h.StartedAt = cutover
					h.Deadline = cutover.Add(api.ProjectReleaseVerificationTimeout)
					h.CheckedAt = nil
					setPromotionHealthTestCheckpoint(t, store, f.promotion.ID, h)
					for i := 1; i < len(f.apps); i++ {
						member := promotionWorkerSetMember(f, i)
						SeedProjectWorkerJobSamplesForTest(t, store, f.apps[i].ID, f.account.ID, member.view.Worker.BindingID, f.previous[i].ID, cutover.Add(-time.Second), false)
					}
					failed, omit := 0, 0
					if scenario == "last_regression" || scenario == "rollback_startup" {
						failed = len(f.apps) - 1
					}
					if scenario == "last_health_missing" {
						omit = len(f.apps) - 1
					}
					seedPromotionWorkerSetCandidate(t, f, failed, omit, cutover.Add(time.Second))
					h = promotionWorkerRound(t, f)
					seedPromotionWorkerSetCandidate(t, f, failed, omit)
					h = promotionWorkerRound(t, f)
					if scenario == "last_health_missing" {
						if h.Phase != "verifying" || h.Verdict != "unknown" {
							t.Fatal("subset of delivery health became successful", h)
						}
						return
					}
					if failed != 0 {
						if h.Phase != "rolling_back" {
							t.Fatal("last worker regression did not restore full release", h)
						}
						members := coordinatedBaselineMembers(f)
						if scenario == "rollback_startup" {
							for i := 1; i < len(f.apps)-1; i++ {
								seedPromotionWorkerRuntime(t, promotionWorkerSetMember(f, i), f.previous[i].ID)
							}
							if _, err = store.(ProjectEnvironmentPromotionReleaseSetStore).RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, members); !errors.Is(err, ErrPromotionWorkerNotReady) {
								t.Fatal("restoration ignored missing last baseline worker", err)
							}
						}
						for i := 1; i < len(f.apps); i++ {
							if scenario == "rollback_startup" && i < len(f.apps)-1 {
								continue
							}
							seedPromotionWorkerRuntime(t, promotionWorkerSetMember(f, i), f.previous[i].ID)
						}
						_, err = store.(ProjectEnvironmentPromotionReleaseSetStore).RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, members)
						requirePromotionSnapshot(t, err)
						return
					}
					if h.Phase != "finalizing" {
						t.Fatal("healthy workers did not reach finalization", h)
					}
					_, err = store.(ProjectPromotionHealthStore).AdvanceProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: h.Generation, Action: "complete"})
					requirePromotionSnapshot(t, err)
				})
			}
		})
	}
}
