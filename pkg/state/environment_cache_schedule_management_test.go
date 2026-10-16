package state_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCacheScheduleManagementMemory(t *testing.T) { testScheduleManagement(t, state.NewMemStore()) }
func TestCacheScheduleManagementPostgres(t *testing.T) {
	testScheduleManagement(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testScheduleManagement(t *testing.T, s state.Store) {
	ctx := t.Context()
	account, e := s.CreateAccount(ctx, uuid.NewString()+"@reschedule.test", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	project, e := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "reschedule"})
	if e != nil {
		t.Fatal(e)
	}
	app, e := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "reschedule", Type: state.AppTypeApp, RAMMB: 256})
	if e != nil {
		t.Fatal(e)
	}
	env, e := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if e != nil {
		t.Fatal(e)
	}
	store := s.(state.EnvironmentCacheScheduleStore)
	management := s.(state.EnvironmentCacheScheduleManagement)
	worker := s.(state.EnvironmentCacheScheduleWorker)
	create := func(at time.Time) api.EnvironmentCacheSchedule {
		t.Helper()
		id := uuid.NewString()
		v, e := store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", api.CreateEnvironmentCacheScheduleRequest{ScheduleID: id, AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, ExecuteAt: at, Purge: &api.CreateEnvironmentCachePurgeRequest{RequestID: id, AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, Path: "/item"}})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	a, b, c := create(now.Add(time.Hour)), create(now.Add(time.Hour)), create(now.Add(2*time.Hour))
	o := api.EnvironmentCacheScheduleListOptions{Status: "scheduled", Limit: 1}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		page, e := management.ListEnvironmentCacheSchedules(ctx, account.ID, app.ID, "staging", o)
		if e != nil || len(page.Entries) != 1 || seen[page.Entries[0].ScheduleID] || page.EnvironmentID != env.ID {
			t.Fatal("page", page, e)
		}
		seen[page.Entries[0].ScheduleID] = true
		if i == 0 {
			bad := o
			bad.After = page.NextCursor
			bad.Status = "cancelled"
			if _, e := management.ListEnvironmentCacheSchedules(ctx, account.ID, app.ID, "staging", bad); !errors.Is(e, state.ErrInvalidArgument) {
				t.Fatal("cursor filter escape", e)
			}
			if _, e := management.ListEnvironmentCacheSchedules(ctx, account.ID, app.ID, "production", api.EnvironmentCacheScheduleListOptions{Status: "scheduled", After: page.NextCursor}); !errors.Is(e, state.ErrInvalidArgument) {
				t.Fatal("cursor environment escape", e)
			}
		}
		o.After = page.NextCursor
		if i < 2 && o.After == "" || i == 2 && o.After != "" {
			t.Fatal("bad continuation")
		}
	}
	until := now.Add(2 * time.Hour)
	from := now.Add(time.Hour)
	page, e := management.ListEnvironmentCacheSchedules(ctx, account.ID, app.ID, "staging", api.EnvironmentCacheScheduleListOptions{From: &from, Until: &until})
	if e != nil || len(page.Entries) != 2 {
		t.Fatal("time filter", page, e)
	}
	request := func(old api.EnvironmentCacheSchedule) api.RescheduleEnvironmentCacheRequest {
		return api.RescheduleEnvironmentCacheRequest{ReplacementID: uuid.NewString(), AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, ExpectedExecuteAt: old.ExecuteAt, ExecuteAt: now.Add(3 * time.Hour)}
	}
	req := request(a)
	next, e := management.RescheduleEnvironmentCache(ctx, account.ID, app.ID, "staging", a.ScheduleID, req)
	if e != nil || next.Status != "scheduled" || next.ReplacesScheduleID != a.ScheduleID || next.Purge.Path != "/item" || next.Purge.RequestID != next.ScheduleID {
		t.Fatal("replace", next, e)
	}
	old, e := store.GetEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", a.ScheduleID)
	if e != nil || old.Status != "cancelled" || old.ReplacedByScheduleID != next.ScheduleID || old.FinishedAt == nil {
		t.Fatal("history", old, e)
	}
	// Recovery is stable even after the replacement has become terminal.
	if _, e := store.CancelEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", next.ScheduleID); e != nil {
		t.Fatal(e)
	}
	replay, e := management.RescheduleEnvironmentCache(ctx, account.ID, app.ID, "staging", a.ScheduleID, req)
	if e != nil || replay.ScheduleID != next.ScheduleID || replay.Status != "cancelled" {
		t.Fatal("retry", replay, e)
	}
	changed := req
	changed.ExecuteAt = changed.ExecuteAt.Add(time.Hour)
	if _, e := management.RescheduleEnvironmentCache(ctx, account.ID, app.ID, "staging", a.ScheduleID, changed); !errors.Is(e, state.ErrConflict) {
		t.Fatal("changed retry", e)
	}
	// A conflicting UUID must roll back original cancellation.
	collision := request(b)
	collision.ReplacementID = c.ScheduleID
	if _, e := management.RescheduleEnvironmentCache(ctx, account.ID, app.ID, "staging", b.ScheduleID, collision); !errors.Is(e, state.ErrConflict) {
		t.Fatal("UUID collision", e)
	}
	unchanged, e := store.GetEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", b.ScheduleID)
	if e != nil || unchanged.Status != "scheduled" || unchanged.ReplacedByScheduleID != "" {
		t.Fatal("partial cancellation", unchanged, e)
	}

	// A UUID owned by another environment reaches the SQL unique-conflict rollback.
	production, e := worker.GetEnvironmentCachePolicy(ctx, account.ID, app.ID, "production")
	if e != nil {
		t.Fatal(e)
	}
	foreignID := uuid.NewString()
	_, e = store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "production", api.CreateEnvironmentCacheScheduleRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: production.EnvironmentID, ScheduleID: foreignID, ExecuteAt: now.Add(time.Hour), Purge: &api.CreateEnvironmentCachePurgeRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: production.EnvironmentID, RequestID: foreignID, Path: ""}})
	if e != nil {
		t.Fatal(e)
	}
	collision.ReplacementID = foreignID
	if _, e := management.RescheduleEnvironmentCache(ctx, account.ID, app.ID, "staging", b.ScheduleID, collision); !errors.Is(e, state.ErrConflict) {
		t.Fatal("cross-environment UUID collision", e)
	}
	unchanged, e = store.GetEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", b.ScheduleID)
	if e != nil || unchanged.Status != "scheduled" || unchanged.ReplacedByScheduleID != "" {
		t.Fatal("SQL rollback lost original", unchanged, e)
	}
	// Two simultaneous replacements may create only one successor.
	r1, r2 := request(b), request(b)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, r := range []api.RescheduleEnvironmentCacheRequest{r1, r2} {
		wg.Go(func() {
			_, e := management.RescheduleEnvironmentCache(ctx, account.ID, app.ID, "staging", b.ScheduleID, r)
			errs <- e
		})
	}
	wg.Wait()
	close(errs)
	successes := 0
	for e := range errs {
		if e == nil {
			successes++
		} else if !errors.Is(e, state.ErrConflict) {
			t.Fatal(e)
		}
	}
	if successes != 1 {
		t.Fatal("multiple successors", successes)
	}
	// Pending policy guard and complete rule body are retained, never silently rebased.
	zero := int64(0)
	id := uuid.NewString()
	policy := api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, RequestID: id, ExpectedRevision: &zero, Rules: []api.EnvironmentCacheRule{}, Inherited: true}
	scheduled, e := store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", api.CreateEnvironmentCacheScheduleRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, ScheduleID: id, ExecuteAt: now.Add(time.Hour), Policy: &policy})
	if e != nil {
		t.Fatal(e)
	}
	policyNext, e := management.RescheduleEnvironmentCache(ctx, account.ID, app.ID, "staging", id, request(scheduled))
	if e != nil || *policyNext.Policy.ExpectedRevision != 0 || !policyNext.Policy.Inherited || !reflect.DeepEqual(policyNext.Policy.Rules, policy.Rules) {
		t.Fatal("policy changed", policyNext, e)
	}
	edit := policy
	edit.RequestID = uuid.NewString()
	if _, e := worker.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", edit); e != nil {
		t.Fatal(e)
	}
	stale := request(policyNext)
	stale.ExecuteAt = now.Add(4 * time.Hour)
	if _, e := management.RescheduleEnvironmentCache(ctx, account.ID, app.ID, "staging", policyNext.ScheduleID, stale); !errors.Is(e, state.ErrRouteHealthRevision) {
		t.Fatal("policy silently rebased", e)
	}
	for i := 0; i < 3; i++ {
		due := create(time.Now().Add(time.Second))
		time.Sleep(time.Until(due.ExecuteAt) + 10*time.Millisecond)
		replace := request(due)
		var claim state.EnvironmentCacheScheduleClaim
		var claimErr, replaceErr error
		wg.Go(func() { claim, claimErr = worker.ClaimEnvironmentCacheSchedule(ctx) })
		wg.Go(func() {
			_, replaceErr = management.RescheduleEnvironmentCache(ctx, account.ID, app.ID, "staging", due.ScheduleID, replace)
		})
		wg.Wait()
		if replaceErr == nil {
			if !errors.Is(claimErr, state.ErrNotFound) {
				t.Fatal("original also claimed", claim, claimErr)
			}
		} else {
			if !errors.Is(replaceErr, state.ErrConflict) || claimErr != nil || claim.Schedule.ScheduleID != due.ScheduleID {
				t.Fatal("claim race", replaceErr, claimErr)
			}
			result := claim.Schedule
			result.Status = "failed"
			result.ErrorCode = "test_claim"
			if e := worker.FinishEnvironmentCacheSchedule(ctx, claim, result); e != nil {
				t.Fatal(e)
			}
		}
	}
	// Replacing at the active-schedule cap must consume the original slot.
	active := 0
	options := api.EnvironmentCacheScheduleListOptions{Status: "scheduled", Limit: api.EnvironmentCacheSchedulePageMax}
	for {
		page, e := management.ListEnvironmentCacheSchedules(ctx, account.ID, app.ID, "staging", options)
		if e != nil {
			t.Fatal(e)
		}
		active += len(page.Entries)
		if page.NextCursor == "" {
			break
		}
		options.After = page.NextCursor
	}
	for active < api.EnvironmentCacheScheduleLimit {
		create(now.Add(24 * time.Hour))
		active++
	}
	atCap, e := management.RescheduleEnvironmentCache(ctx, account.ID, app.ID, "staging", c.ScheduleID, request(c))
	if e != nil || atCap.Status != "scheduled" {
		t.Fatal("replacement consumed extra quota", atCap, e)
	}
	extra, e := api.EnvironmentCacheScheduleReplacement(atCap, uuid.NewString(), now.Add(4*time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	if _, e := store.CreateEnvironmentCacheSchedule(ctx, account.ID, app.ID, "staging", extra); !errors.Is(e, state.ErrInvalidArgument) {
		t.Fatal("active quota not retained", e)
	}

}
