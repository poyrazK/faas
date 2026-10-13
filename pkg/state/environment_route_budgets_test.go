// adr: 1252
package state_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentRouteBudgetsMem(t *testing.T) { testEnvironmentBudgets(t, state.NewMemStore()) }
func TestEnvironmentRouteBudgetsPG(t *testing.T) {
	testEnvironmentBudgets(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testEnvironmentBudgets(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@budgets.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "budget-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "budget-app", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: p.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	monitor := s.(state.RouteMonitorStore)
	budgets := s.(state.EnvironmentRouteBudgetStore)
	rate := int64(100)
	parent, err := monitor.SetRouteMonitor(ctx, a.ID, app.ID, api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: new(int64), Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/checkout", Max5xxRateBPS: &rate, MaxP95MS: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := budgets.GetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging")
	if err != nil || before.BudgetSource != "app_policy" || before.Revision != 0 || before.AppRevision != parent.Revision || before.EnvironmentID != env.ID {
		t.Fatalf("inheritance=%+v %v", before, err)
	}
	req := api.SetEnvironmentRouteBudgetsRequest{AppID: app.ID, ProjectID: p.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &before.Revision, ExpectedAppRevision: &before.AppRevision, Enabled: true, Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/checkout", MaxP95MS: 500}}}
	saved, err := budgets.SetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging", req)
	if err != nil || saved.Revision != 1 || saved.Config.Routes[0].MaxP95MS != 500 || saved.BudgetSource != "environment_policy" {
		t.Fatalf("override=%+v %v", saved, err)
	}
	other, err := budgets.GetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "default")
	if err != nil || other.BudgetSource != "app_policy" || other.Config.Routes[0].MaxP95MS != 100 || other.Revision != 0 {
		t.Fatal("staging update changed default")
	}
	// Two contenders share an expected revision. Exactly one may win.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		change := req
		change.RequestID = uuid.NewString()
		change.ExpectedRevision = &saved.Revision
		wg.Go(func() {
			_, err := budgets.SetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging", change)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	wins, stale := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, state.ErrRouteHealthRevision) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatal("concurrent updates were not serialized")
	}
	replay, err := budgets.SetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging", req)
	if err != nil || replay.Revision != 1 {
		t.Fatal("lost reply did not replay original receipt after later update")
	}
	tampered := req
	tampered.Enabled = false
	if _, err := budgets.SetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging", tampered); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("key payload reuse=%v", err)
	}
	if _, err := budgets.GetEnvironmentRouteBudgets(ctx, uuid.NewString(), app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account read")
	}
	fresh, err := budgets.GetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	// Parent edits invalidate a plan even when the target currently overrides.
	_, err = monitor.SetRouteMonitor(ctx, a.ID, app.ID, api.SetRouteMonitorRequest{Enabled: false, ExpectedRevision: &parent.Revision, Routes: []api.RouteMonitorRoute{}})
	if err != nil {
		t.Fatal(err)
	}
	restore := req
	restore.RequestID = uuid.NewString()
	restore.ExpectedRevision = &fresh.Revision
	restore.Inherit, restore.Enabled, restore.Routes = true, false, []api.RouteMonitorRoute{}
	if _, err := budgets.SetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging", restore); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatal("changed parent policy did not reject stale plan")
	}
	fresh, err = budgets.GetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	restore.ExpectedAppRevision = &fresh.AppRevision
	restored, err := budgets.SetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging", restore)
	if err != nil || restored.BudgetSource != "app_policy" || restored.Config.Enabled || restored.Revision != 3 {
		t.Fatalf("restore=%+v %v", restored, err)
	}
	if err := s.DeleteProjectEnvironment(ctx, a.ID, p.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	recreated, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: p.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := budgets.GetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging")
	if err != nil || current.EnvironmentID != recreated.ID || current.Revision != 0 || current.BudgetSource != "app_policy" {
		t.Fatal("recreated environment inherited override")
	}
	if _, err := budgets.SetEnvironmentRouteBudgets(ctx, a.ID, app.ID, "staging", req); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatal("old receipt crossed environment incarnation")
	}
}
