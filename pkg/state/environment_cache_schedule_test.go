package state_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/cacheschedule"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentCacheScheduleMemory(t *testing.T) { testCacheSchedule(t, state.NewMemStore(), nil) }
func TestEnvironmentCacheSchedulePostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	testCacheSchedule(t, state.NewPgStore(pool), func(id string) {
		if _, err := pool.Exec(t.Context(), "UPDATE environment_cache_schedules SET lease_until=clock_timestamp()-interval '1 second' WHERE schedule_id=$1::uuid", id); err != nil {
			t.Fatal(err)
		}
	})
}
func testCacheSchedule(t *testing.T, s state.Store, expire func(string)) {
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@schedule.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "schedules"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "schedules", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	store := s.(state.EnvironmentCacheScheduleStore)
	worker := s.(state.EnvironmentCacheScheduleWorker)
	makePurge := func(at time.Time) api.CreateEnvironmentCacheScheduleRequest {
		id := uuid.NewString()
		return api.CreateEnvironmentCacheScheduleRequest{ScheduleID: id, AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, ExecuteAt: at, Purge: &api.CreateEnvironmentCachePurgeRequest{RequestID: id, AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, Path: "/item"}}
	}
	req := makePurge(time.Now().Add(time.Hour))
	first, err := store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", req)
	if err != nil || first.Status != "scheduled" {
		t.Fatalf("accept=%+v %v", first, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Go(func() {
			out, e := store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", req)
			if e != nil || !out.CreatedAt.Equal(first.CreatedAt) {
				t.Errorf("retry=%+v %v", out, e)
			}
		})
	}
	wg.Wait()

	neighbor, e := worker.GetEnvironmentCachePolicy(ctx, account.ID, app.ID, "production")
	if e != nil {
		t.Fatal(e)
	}
	wrongTarget := req
	purgeCopy := *req.Purge
	wrongTarget.Purge = &purgeCopy
	wrongTarget.EnvironmentID = neighbor.EnvironmentID
	wrongTarget.Purge.EnvironmentID = neighbor.EnvironmentID
	if _, e := store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "production", wrongTarget); !errors.Is(e, state.ErrConflict) {
		t.Fatalf("schedule UUID target reuse=%v", e)
	}
	req.Purge.Path = "/changed"
	if _, e := store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", req); !errors.Is(e, state.ErrConflict) {
		t.Fatalf("changed request=%v", e)
	}
	if _, e := store.GetEnvironmentCacheSchedule(ctx, account.ID, app.ID, "production", req.ScheduleID); !errors.Is(e, state.ErrNotFound) {
		t.Fatalf("scope leak=%v", e)
	}
	if _, e := worker.ClaimEnvironmentCacheSchedule(ctx); !errors.Is(e, state.ErrNotFound) {
		t.Fatalf("early execution=%v", e)
	}
	for i := 0; i < 2; i++ {
		out, e := store.CancelEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", req.ScheduleID)
		if e != nil || out.Status != "cancelled" {
			t.Fatal("cancel", out, e)
		}
	}
	// Due purge: simulate process death after the action commits, before completion.
	req = makePurge(time.Now().Add(time.Second))
	_, err = store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(req.ExecuteAt) + 10*time.Millisecond)
	claim, err := worker.ClaimEnvironmentCacheSchedule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, e := store.CancelEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", req.ScheduleID); !errors.Is(e, state.ErrConflict) {
		t.Fatalf("cancel executing=%v", e)
	}
	receipt, err := worker.CreateEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", *claim.Schedule.Purge)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := worker.CreateEnvironmentCachePurge(ctx, account.ID, app.ID, "staging", *claim.Schedule.Purge)
	if err != nil || replay.Generation != receipt.Generation {
		t.Fatal("action duplicated", err)
	}
	result := claim.Schedule
	result.Status = "executed"
	result.PurgeResult = &replay
	wrong := claim
	wrong.LeaseToken = uuid.NewString()
	if e := worker.FinishEnvironmentCacheSchedule(ctx, wrong, result); !errors.Is(e, state.ErrConflict) {
		t.Fatalf("stale worker=%v", e)
	}
	if expire != nil {
		expire(req.ScheduleID)
		if worked, e := cacheschedule.RunOne(ctx, worker); !worked || e != nil {
			t.Fatal("lease recovery", worked, e)
		}
		if e := worker.FinishEnvironmentCacheSchedule(ctx, claim, result); !errors.Is(e, state.ErrConflict) {
			t.Fatal("old lease completed", e)
		}
	} else {
		recovered, found, e := worker.RecoverEnvironmentCacheScheduleAction(ctx, claim)
		if e != nil || !found || recovered.PurgeResult == nil || recovered.PurgeResult.Generation != receipt.Generation {
			t.Fatal("receipt recovery", recovered, found, e)
		}
		if e := worker.FinishEnvironmentCacheSchedule(ctx, claim, result); e != nil {
			t.Fatal(e)
		}
	}
	out, e := store.GetEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", req.ScheduleID)
	if e != nil || out.Status != "executed" || out.PurgeResult.Generation != receipt.Generation {
		t.Fatal(out, e)
	}
	// A scheduled policy must fail if the desired revision changes before execution.
	zero := int64(0)
	id := uuid.NewString()
	policy := api.SetEnvironmentCachePolicyRequest{RequestID: id, AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, ExpectedRevision: &zero, Rules: []api.EnvironmentCacheRule{}}
	scheduled := api.CreateEnvironmentCacheScheduleRequest{ScheduleID: id, AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, ExecuteAt: time.Now().Add(time.Second), Policy: &policy}
	if _, e := store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", scheduled); e != nil {
		t.Fatal(e)
	}
	edit := policy
	edit.RequestID = uuid.NewString()
	if _, e := worker.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", edit); e != nil {
		t.Fatal(e)
	}
	time.Sleep(time.Until(scheduled.ExecuteAt) + 10*time.Millisecond)
	if worked, e := cacheschedule.RunOne(ctx, worker); !worked || e != nil {
		t.Fatal("run stale", worked, e)
	}
	out, e = store.GetEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", id)
	if e != nil || out.Status != "failed" || out.ErrorCode != "policy_or_target_changed" {
		t.Fatal("stale policy executed", out, e)
	}
	// Valid inherited policy execution creates a fresh revision, never marks convergence.
	one := int64(1)
	id = uuid.NewString()
	policy.RequestID = id
	policy.ExpectedRevision = &one
	policy.Inherited = true
	scheduled.ScheduleID = id
	scheduled.Policy = &policy
	scheduled.ExecuteAt = time.Now().Add(time.Second)
	if _, e := store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", scheduled); e != nil {
		t.Fatal(e)
	}
	time.Sleep(time.Until(scheduled.ExecuteAt) + 10*time.Millisecond)
	if worked, e := cacheschedule.RunOne(ctx, worker); !worked || e != nil {
		t.Fatal("run valid", worked, e)
	}
	out, e = store.GetEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", id)
	if e != nil || out.Status != "executed" || out.PolicyResult == nil || out.PolicyResult.Revision != 2 || !out.PolicyResult.Inherited {
		t.Fatal("policy execution", out, e)
	}
	for _, at := range []time.Time{time.Now().Add(-time.Hour), time.Now().Add(api.EnvironmentCacheScheduleMaxDelay + time.Hour)} {
		if _, e := store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", makePurge(at)); !errors.Is(e, state.ErrInvalidArgument) {
			t.Fatalf("invalid time=%v", e)
		}
	}
}
