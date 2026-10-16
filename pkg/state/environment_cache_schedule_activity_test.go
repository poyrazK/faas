package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/cacheschedule"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type activityFixture struct {
	Store   state.Store
	Account state.Account
	Project state.Project
	App     state.App
	Env     state.ProjectEnvironment
}

func newActivityFixture(t *testing.T, s state.Store) *activityFixture {
	t.Helper()
	f := &activityFixture{Store: s}
	var e error
	f.Account, e = s.CreateAccount(t.Context(), uuid.NewString()+"@activity.test", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	f.Project, e = s.CreateProject(t.Context(), state.Project{AccountID: f.Account.ID, Slug: "activity"})
	if e != nil {
		t.Fatal(e)
	}
	f.App, e = s.CreateApp(t.Context(), state.App{AccountID: f.Account.ID, ProjectID: f.Project.ID, Slug: "activity", Type: state.AppTypeApp, RAMMB: 256})
	if e != nil {
		t.Fatal(e)
	}
	f.Env, e = s.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: f.Account.ID, ProjectID: f.Project.ID, Slug: "staging"})
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *activityFixture) proposal(at time.Time) api.CreateEnvironmentCacheScheduleRequest {
	id := uuid.NewString()
	return api.CreateEnvironmentCacheScheduleRequest{ScheduleID: id, AppID: f.App.ID, ProjectID: f.Project.ID, EnvironmentID: f.Env.ID, ExecuteAt: at, Purge: &api.CreateEnvironmentCachePurgeRequest{RequestID: id, AppID: f.App.ID, ProjectID: f.Project.ID, EnvironmentID: f.Env.ID, Path: "/private-catalogue/item"}}
}
func (f *activityFixture) create(t *testing.T, r api.CreateEnvironmentCacheScheduleRequest) api.EnvironmentCacheSchedule {
	t.Helper()
	out, e := f.Store.(state.EnvironmentCacheScheduleStore).CreateEnvironmentCacheSchedule(t.Context(), f.Account.ID, f.App.ID, "staging", r)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func (f *activityFixture) events(t *testing.T, after string, limit int) api.EnvironmentCacheScheduleActivity {
	t.Helper()
	out, e := f.Store.(state.EnvironmentCacheScheduleActivityReader).GetEnvironmentCacheScheduleActivity(t.Context(), f.Account.ID, f.App.ID, "staging", after, limit)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestCacheScheduleActivityMemory(t *testing.T) { testScheduleActivity(t, state.NewMemStore()) }
func TestCacheScheduleActivityPostgres(t *testing.T) {
	testScheduleActivity(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testScheduleActivity(t *testing.T, s state.Store) {
	f := newActivityFixture(t, s)
	ctx := t.Context()
	store := s.(state.EnvironmentCacheScheduleStore)
	worker := s.(state.EnvironmentCacheScheduleWorker)
	reader := s.(state.EnvironmentCacheScheduleActivityReader)
	empty := f.events(t, "", 1)
	if empty.Entries == nil || len(empty.Entries) != 0 || empty.Cursor == "" || empty.HasMore {
		t.Fatal("empty checkpoint", empty)
	}
	req := f.proposal(time.Now().Add(time.Hour))
	a := f.create(t, req)
	f.create(t, req)
	b := f.create(t, f.proposal(time.Now().Add(time.Hour)))
	for i := 0; i < 2; i++ {
		if _, e := store.CancelEnvironmentCacheSchedule(ctx, f.Account.ID, f.App.ID, "staging", a.ScheduleID); e != nil {
			t.Fatal(e)
		}
	}
	replace := api.RescheduleEnvironmentCacheRequest{ReplacementID: uuid.NewString(), AppID: f.App.ID, ProjectID: f.Project.ID, EnvironmentID: f.Env.ID, ExpectedExecuteAt: b.ExecuteAt, ExecuteAt: b.ExecuteAt.Add(time.Hour)}
	var next api.EnvironmentCacheSchedule
	for i := 0; i < 2; i++ {
		var e error
		next, e = s.(state.EnvironmentCacheScheduleManagement).RescheduleEnvironmentCache(ctx, f.Account.ID, f.App.ID, "staging", b.ScheduleID, replace)
		if e != nil {
			t.Fatal(e)
		}
	}
	cursor := empty.Cursor
	kinds := []string{}
	var all []api.EnvironmentCacheScheduleEvent
	for {
		page := f.events(t, cursor, 2)
		for _, e := range page.Entries {
			if e.Sequence != int64(len(kinds)+1) {
				t.Fatal("sequence gap", e)
			}
			kinds = append(kinds, e.Kind)
			all = append(all, e)
		}
		cursor = page.Cursor
		if !page.HasMore {
			break
		}
	}
	if !reflect.DeepEqual(kinds, []string{"accepted", "accepted", "cancelled", "rescheduled", "accepted"}) {
		t.Fatal("duplicate or missing transitions", kinds)
	}
	if all[0].Status != "scheduled" || all[0].RelatedScheduleID != "" || all[3].RelatedScheduleID != next.ScheduleID || all[4].RelatedScheduleID != b.ScheduleID {
		t.Fatal("event snapshot or lineage changed", all)
	}
	raw, _ := json.Marshal(all)
	if strings.Contains(string(raw), "private-catalogue") || strings.Contains(string(raw), `"path"`) || strings.Contains(string(raw), `"rules"`) {
		t.Fatal("activity disclosed action bodies")
	}
	caught := f.events(t, cursor, 2)
	if len(caught.Entries) != 0 || caught.Cursor != cursor {
		t.Fatal("tail checkpoint changed", caught)
	}
	if _, e := store.CancelEnvironmentCacheSchedule(ctx, f.Account.ID, f.App.ID, "staging", next.ScheduleID); e != nil {
		t.Fatal(e)
	}
	continued := f.events(t, cursor, 2)
	if len(continued.Entries) != 1 || continued.Entries[0].Kind != "cancelled" || continued.Entries[0].Sequence != 6 {
		t.Fatal("reconnect missed event", continued)
	}
	if replay := f.events(t, cursor, 2); !reflect.DeepEqual(continued, replay) {
		t.Fatal("activity replay is not stable")
	}
	c, e := api.ParseEnvironmentCacheActivityCursor(continued.Cursor, f.Account.ID, f.App.ID, f.Project.ID, f.Env.ID)
	if e != nil {
		t.Fatal(e)
	}
	c.Sequence += 100
	if _, e = reader.GetEnvironmentCacheScheduleActivity(ctx, f.Account.ID, f.App.ID, "staging", api.EncodeEnvironmentCacheActivityCursor(c), 2); !errors.Is(e, state.ErrInvalidArgument) {
		t.Fatal("future cursor accepted", e)
	}
	if _, e = reader.GetEnvironmentCacheScheduleActivity(ctx, f.Account.ID, f.App.ID, "production", cursor, 2); !errors.Is(e, state.ErrInvalidArgument) {
		t.Fatal("cursor escaped environment", e)
	}
	if _, e = reader.GetEnvironmentCacheScheduleActivity(ctx, uuid.NewString(), f.App.ID, "staging", cursor, 2); !errors.Is(e, state.ErrNotFound) {
		t.Fatal("foreign account read", e)
	}
	// A stale completion lease emits nothing. Exactly one confirmed outcome follows.
	due := f.create(t, f.proposal(time.Now().Add(time.Second)))
	time.Sleep(time.Until(due.ExecuteAt) + 10*time.Millisecond)
	claim, e := worker.ClaimEnvironmentCacheSchedule(ctx)
	if e != nil {
		t.Fatal(e)
	}
	tail := f.events(t, "", 50)
	if len(tail.Entries) != 7 {
		t.Fatal("claim published a synthetic outcome", tail)
	}
	receipt, e := worker.CreateEnvironmentCachePurge(ctx, f.Account.ID, f.App.ID, "staging", *claim.Schedule.Purge)
	if e != nil {
		t.Fatal(e)
	}
	finished := claim.Schedule
	finished.Status = "executed"
	finished.PurgeResult = &receipt
	wrong := claim
	wrong.LeaseToken = uuid.NewString()
	if e = worker.FinishEnvironmentCacheSchedule(ctx, wrong, finished); !errors.Is(e, state.ErrConflict) {
		t.Fatal(e)
	}
	if page := f.events(t, tail.Cursor, 50); len(page.Entries) != 0 {
		t.Fatal("stale completion emitted", page)
	}
	if e = worker.FinishEnvironmentCacheSchedule(ctx, claim, finished); e != nil {
		t.Fatal(e)
	}
	if e = worker.FinishEnvironmentCacheSchedule(ctx, claim, finished); !errors.Is(e, state.ErrConflict) {
		t.Fatal("completion replay", e)
	}
	outcome := f.events(t, tail.Cursor, 50)
	if len(outcome.Entries) != 1 || outcome.Entries[0].Kind != "executed" || outcome.Entries[0].PurgeGeneration == nil || *outcome.Entries[0].PurgeGeneration != receipt.Generation {
		t.Fatal("execution receipt missing", outcome)
	}
	zero := int64(0)
	p := f.proposal(time.Now().Add(time.Second))
	p.Purge = nil
	p.Policy = &api.SetEnvironmentCachePolicyRequest{AppID: f.App.ID, ProjectID: f.Project.ID, EnvironmentID: f.Env.ID, RequestID: p.ScheduleID, ExpectedRevision: &zero, Rules: []api.EnvironmentCacheRule{}, Inherited: true}
	scheduled := f.create(t, p)
	edit := *p.Policy
	edit.RequestID = uuid.NewString()
	if _, e = worker.SetEnvironmentCachePolicy(ctx, f.Account.ID, f.App.ID, "staging", edit); e != nil {
		t.Fatal(e)
	}
	time.Sleep(time.Until(scheduled.ExecuteAt) + 10*time.Millisecond)
	if worked, e := cacheschedule.RunOne(ctx, worker); !worked || e != nil {
		t.Fatal(worked, e)
	}
	failed := f.events(t, outcome.Cursor, 50)
	if len(failed.Entries) != 2 || failed.Entries[1].Kind != "failed" || failed.Entries[1].ErrorCode != "policy_or_target_changed" {
		t.Fatal("missed failed action", failed)
	}
	one := int64(1)
	p.ScheduleID = uuid.NewString()
	p.Policy.RequestID = p.ScheduleID
	p.Policy.ExpectedRevision = &one
	p.ExecuteAt = time.Now().Add(time.Second)
	scheduled = f.create(t, p)
	time.Sleep(time.Until(scheduled.ExecuteAt) + 10*time.Millisecond)
	if worked, e := cacheschedule.RunOne(ctx, worker); !worked || e != nil {
		t.Fatal(worked, e)
	}
	applied := f.events(t, failed.Cursor, 50)
	if len(applied.Entries) != 2 || applied.Entries[1].Action != "inheritance" || applied.Entries[1].PolicyRevision == nil || *applied.Entries[1].PolicyRevision != 2 {
		t.Fatal("missing policy revision", applied)
	}

	// Completion still records a rejected action while its app is soft-deleted.
	hidden := f.create(t, f.proposal(time.Now().Add(time.Second)))
	hiddenTail := f.events(t, "", 50)
	if _, e = s.SoftDeleteAppCascade(ctx, f.App.ID); e != nil {
		t.Fatal(e)
	}
	time.Sleep(time.Until(hidden.ExecuteAt) + 10*time.Millisecond)
	if worked, e := cacheschedule.RunOne(ctx, worker); !worked || e != nil {
		t.Fatal("soft-deleted completion could not commit", worked, e)
	}
	if _, e = reader.GetEnvironmentCacheScheduleActivity(ctx, f.Account.ID, f.App.ID, "staging", hiddenTail.Cursor, 50); !errors.Is(e, state.ErrNotFound) {
		t.Fatal("deleted app exposed activity", e)
	}
	if _, e = s.RestoreApp(ctx, f.App.ID, api.MustLimitsFor(api.PlanPro)); e != nil {
		t.Fatal(e)
	}
	restored := f.events(t, hiddenTail.Cursor, 50)
	if len(restored.Entries) != 1 || restored.Entries[0].Kind != "failed" || restored.Entries[0].ErrorCode != "target_not_found" {
		t.Fatal("failure disappeared across restore", restored)
	}
	// Owner deletion removes feed state; recreating a slug cannot reuse the checkpoint.
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	if e = s.DeleteProjectEnvironment(ctx, f.Account.ID, f.Project.ID, "staging"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.Account.ID, ProjectID: f.Project.ID, Slug: "staging"}); e != nil {
		t.Fatal(e)
	}
	if _, e = reader.GetEnvironmentCacheScheduleActivity(ctx, f.Account.ID, f.App.ID, "staging", applied.Cursor, 50); !errors.Is(e, state.ErrInvalidArgument) {
		t.Fatal("recreated environment reused cursor", e)
	}
	if page := f.events(t, "", 50); len(page.Entries) != 0 {
		t.Fatal("old events leaked", page)
	}
	if _, e = worker.ClaimEnvironmentCacheSchedule(ctx); !errors.Is(e, state.ErrNotFound) {
		t.Fatal("deleted schedule survived", e)
	}
}

func TestCacheScheduleActivityPostgresAtomicRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	f := newActivityFixture(t, state.NewPgStore(pool))
	ctx := t.Context()
	store := f.Store.(state.EnvironmentCacheScheduleStore)
	worker := f.Store.(state.EnvironmentCacheScheduleWorker)
	old := f.create(t, f.proposal(time.Now().Add(time.Second)))
	before := f.events(t, "", 50)
	if _, e := pool.Exec(ctx, "ALTER TABLE environment_cache_schedule_events ADD CONSTRAINT test_fail_activity CHECK (false) NOT VALID"); e != nil {
		t.Fatal(e)
	}
	req := f.proposal(time.Now().Add(time.Hour))
	if _, e := store.CreateEnvironmentCacheSchedule(ctx, f.Account.ID, f.App.ID, "staging", req); e == nil {
		t.Fatal("create ignored event failure")
	}
	if _, e := store.GetEnvironmentCacheSchedule(ctx, f.Account.ID, f.App.ID, "staging", req.ScheduleID); !errors.Is(e, state.ErrNotFound) {
		t.Fatal("create committed without event", e)
	}
	if _, e := store.CancelEnvironmentCacheSchedule(ctx, f.Account.ID, f.App.ID, "staging", old.ScheduleID); e == nil {
		t.Fatal("cancel ignored event failure")
	}

	if _, e := pool.Exec(ctx, "ALTER TABLE environment_cache_schedule_events DROP CONSTRAINT test_fail_activity"); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, "ALTER TABLE environment_cache_schedule_events ADD CONSTRAINT test_fail_activity CHECK ((payload->>'kind')<>'accepted') NOT VALID"); e != nil {
		t.Fatal(e)
	}
	replacement := api.RescheduleEnvironmentCacheRequest{ReplacementID: uuid.NewString(), AppID: f.App.ID, ProjectID: f.Project.ID, EnvironmentID: f.Env.ID, ExpectedExecuteAt: old.ExecuteAt, ExecuteAt: old.ExecuteAt.Add(time.Hour)}
	if _, e := f.Store.(state.EnvironmentCacheScheduleManagement).RescheduleEnvironmentCache(ctx, f.Account.ID, f.App.ID, "staging", old.ScheduleID, replacement); e == nil {
		t.Fatal("replace ignored event failure")
	}
	saved, e := store.GetEnvironmentCacheSchedule(ctx, f.Account.ID, f.App.ID, "staging", old.ScheduleID)
	if e != nil || saved.Status != "scheduled" || saved.ReplacedByScheduleID != "" {
		t.Fatal("cancel or replace partially committed", saved, e)
	}

	// Reject only the second replacement event, proving the first rolls back too.
	if after := f.events(t, "", 50); !reflect.DeepEqual(before, after) {
		t.Fatal("partial replacement event committed", after)
	}
	if _, e := pool.Exec(ctx, "ALTER TABLE environment_cache_schedule_events DROP CONSTRAINT test_fail_activity"); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, "ALTER TABLE environment_cache_schedule_events ADD CONSTRAINT test_fail_activity CHECK (false) NOT VALID"); e != nil {
		t.Fatal(e)
	}
	time.Sleep(time.Until(old.ExecuteAt) + 10*time.Millisecond)
	claim, e := worker.ClaimEnvironmentCacheSchedule(ctx)
	if e != nil {
		t.Fatal(e)
	}
	outcome := claim.Schedule
	outcome.Status = "failed"
	outcome.ErrorCode = "test_failure"
	if e = worker.FinishEnvironmentCacheSchedule(ctx, claim, outcome); e == nil {
		t.Fatal("completion ignored event failure")
	}
	saved, e = store.GetEnvironmentCacheSchedule(ctx, f.Account.ID, f.App.ID, "staging", old.ScheduleID)
	if e != nil || saved.Status != "executing" {
		t.Fatal("completion partially committed", saved, e)
	}
	if after := f.events(t, "", 50); !reflect.DeepEqual(before, after) {
		t.Fatal("rollback left an event or sequence gap", after)
	}
	if _, e = pool.Exec(ctx, "ALTER TABLE environment_cache_schedule_events DROP CONSTRAINT test_fail_activity"); e != nil {
		t.Fatal(e)
	}
	if e = worker.FinishEnvironmentCacheSchedule(ctx, claim, outcome); e != nil {
		t.Fatal(e)
	}
	after := f.events(t, before.Cursor, 50)
	if len(after.Entries) != 1 || after.Entries[0].Sequence != 2 || after.Entries[0].Kind != "failed" {
		t.Fatal("rolled back sequence skipped", after)
	}
}
func TestCacheScheduleActivityPostgresCommitOrder(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	f := newActivityFixture(t, state.NewPgStore(pool))
	ctx := t.Context()
	old := f.create(t, f.proposal(time.Now().Add(time.Hour)))
	before := f.events(t, "", 50)
	id := func(s string) pgtype.UUID {
		var v pgtype.UUID
		if e := v.Scan(s); e != nil {
			t.Fatal(e)
		}
		return v
	}
	payload, _ := json.Marshal(api.EnvironmentCacheScheduleEvent{ScheduleID: old.ScheduleID, Kind: "accepted", Status: "scheduled", Action: "purge", ExecuteAt: old.ExecuteAt})
	params := sqlc.AppendEnvironmentCacheScheduleEventParams{AccountID: id(f.Account.ID), AppID: id(f.App.ID), ProjectID: id(f.Project.ID), EnvironmentID: id(f.Env.ID), Scope: "staging", Payload: payload}
	tx1, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if e = sqlc.New().AppendEnvironmentCacheScheduleEvent(ctx, tx1, params); e != nil {
		t.Fatal(e)
	}
	// An uncommitted event must not advance a polling consumer's checkpoint.
	pending := f.events(t, before.Cursor, 50)
	if len(pending.Entries) != 0 || pending.Cursor != before.Cursor {
		t.Fatal("uncommitted checkpoint advanced", pending)
	}
	tx2, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = tx2.Rollback(ctx) }()
	done := make(chan error, 1)
	go func() { done <- sqlc.New().AppendEnvironmentCacheScheduleEvent(ctx, tx2, params) }()
	select {
	case e := <-done:
		t.Fatal("later event bypassed uncommitted sequence", e)
	case <-time.After(30 * time.Millisecond):
	}
	if e = tx1.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sequence writer did not resume")
	}
	if e = tx2.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	committed := f.events(t, before.Cursor, 50)
	if len(committed.Entries) != 2 || committed.Entries[0].Sequence != 2 || committed.Entries[1].Sequence != 3 {
		t.Fatal("late commit skipped", committed)
	}
}
