package state_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentCacheHistoryMemory(t *testing.T) {
	testEnvironmentCacheHistory(t, state.NewMemStore())
}
func TestEnvironmentCacheHistoryPostgres(t *testing.T) {
	testEnvironmentCacheHistory(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testEnvironmentCacheHistory(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@cache-history.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "cache-history"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "cache-history", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	store := s.(state.EnvironmentCachePolicyStore)
	history := s.(state.EnvironmentCacheHistoryReader)
	current, err := store.GetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	request := func(p api.EnvironmentCachePolicy) api.SetEnvironmentCachePolicyRequest {
		return api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &current.Revision, Rules: p.Rules, Inherited: p.Inherited}
	}
	write := func(req api.SetEnvironmentCachePolicyRequest) api.EnvironmentCachePolicy {
		t.Helper()
		p, e := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
		if e != nil {
			t.Fatal(e)
		}
		current = p
		return p
	}
	rules := []api.EnvironmentCacheRule{{Path: "/catalog/*", Priority: 1, Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120}}}
	write(request(api.EnvironmentCachePolicy{Rules: rules}))
	write(request(api.EnvironmentCachePolicy{Rules: []api.EnvironmentCacheRule{}}))
	write(request(api.EnvironmentCachePolicy{Inherited: true, Rules: []api.EnvironmentCacheRule{}}))
	page, err := history.ListEnvironmentCachePolicyHistory(ctx, account.ID, app.ID, "staging", 0, 2)
	if err != nil || len(page.Entries) != 2 || page.Entries[0].Revision != 3 || !page.Entries[0].Inherited || page.Entries[1].Revision != 2 || page.Entries[1].Inherited || page.NextBeforeRevision == nil || *page.NextBeforeRevision != 2 {
		t.Fatalf("history=%+v %v", page, err)
	}
	page, err = history.ListEnvironmentCachePolicyHistory(ctx, account.ID, app.ID, "staging", *page.NextBeforeRevision, 2)
	if err != nil || len(page.Entries) != 2 || page.Entries[0].Revision != 1 || page.Entries[1].Revision != 0 || !page.Entries[1].Inherited || page.NextBeforeRevision != nil {
		t.Fatalf("history tail=%+v %v", page, err)
	}
	page.Entries[0].Rules[0].Cache.MaxAgeSeconds = 999
	original, err := history.GetEnvironmentCachePolicyRevision(ctx, account.ID, app.ID, "staging", 1)
	if err != nil || original.Rules[0].Cache.MaxAgeSeconds != 120 {
		t.Fatal("history aliasing", err)
	}
	var first api.SetEnvironmentCachePolicyRequest
	var firstResult api.EnvironmentCachePolicy
	for i, rev := range []int64{1, 2, 3, 0} {
		historical, e := history.GetEnvironmentCachePolicyRevision(ctx, account.ID, app.ID, "staging", rev)
		if e != nil {
			t.Fatal(e)
		}
		req := request(historical)
		expected := current.Revision
		req.ExpectedRevision = &expected
		req.RestoreRevision = &rev
		restored := write(req)
		if restored.Revision != expected+1 || restored.Inherited != historical.Inherited || len(restored.Rules) != len(historical.Rules) {
			t.Fatalf("rollback %d=%+v", rev, restored)
		}
		if i == 0 {
			first = req
			firstResult = restored
		}
	}
	replay, err := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", first)
	got, _ := json.Marshal(replay)
	want, _ := json.Marshal(firstResult)
	if err != nil || string(got) != string(want) {
		t.Fatalf("rollback receipt=%s %v", got, err)
	}
	wrong := request(api.EnvironmentCachePolicy{Rules: []api.EnvironmentCacheRule{}})
	one := int64(1)
	wrong.RestoreRevision = &one
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", wrong); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("forged historical contents=%v", err)
	}
	// Simultaneous rollback and replacement reviews share the current guard.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	expected := current.Revision
	for _, restore := range []bool{false, true} {
		req := request(original)
		req.ExpectedRevision = &expected
		if restore {
			req.RestoreRevision = &one
		}
		wg.Go(func() { _, e := store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req); errs <- e })
	}
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			wins++
		} else if errors.Is(e, state.ErrRouteHealthRevision) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("rollback race wins=%d conflicts=%d", wins, conflicts)
	}
	neighbor, e := history.ListEnvironmentCachePolicyHistory(ctx, account.ID, app.ID, "production", 0, 20)
	if e != nil || len(neighbor.Entries) != 1 || neighbor.Entries[0].Revision != 0 {
		t.Fatal("history leaked to neighbor", e)
	}
	if _, err = history.GetEnvironmentCachePolicyRevision(ctx, uuid.NewString(), app.ID, "staging", 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign history=%v", err)
	}
	if err = s.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	if _, err = history.GetEnvironmentCachePolicyRevision(ctx, account.ID, app.ID, "staging", 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("recreated history=%v", err)
	}
	if _, err = store.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", first); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatalf("recreated rollback=%v", err)
	}
}

func TestEnvironmentCacheHistoryMigrationBackfill(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@history-backfill.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "backfill"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "backfill", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	for _, inherit := range []bool{false, true} {
		req := api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &revision, Rules: []api.EnvironmentCacheRule{}, Inherited: inherit}
		out, e := s.SetEnvironmentCachePolicy(ctx, account.ID, app.ID, "staging", req)
		if e != nil {
			t.Fatal(e)
		}
		revision = out.Revision
	}
	// Re-run the migration against pre-existing intent/receipts in this isolated test database.
	if _, err = pool.Exec(ctx, "DROP TABLE environment_cache_policy_history"); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../migrations/20261015170000001_environment_cache_policy_history.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(raw), "-- +goose Down")[0]
	for attempt := range 2 {
		if _, err = pool.Exec(ctx, up); err != nil {
			t.Fatalf("backfill attempt %d: %v", attempt, err)
		}
		page, err := s.ListEnvironmentCachePolicyHistory(ctx, account.ID, app.ID, "staging", 0, 20)
		if err != nil || len(page.Entries) != 3 || page.Entries[0].Revision != 2 || !page.Entries[0].Inherited || page.Entries[1].Revision != 1 || page.Entries[1].Inherited || page.Entries[2].Revision != 0 {
			t.Fatalf("backfill attempt %d=%+v %v", attempt, page, err)
		}
	}
}
