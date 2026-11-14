// adr: 20261024210000
package state_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentBudgetReceiptMem(t *testing.T) {
	testEnvironmentBudgetReceipt(t, state.NewMemStore())
}
func TestEnvironmentBudgetReceiptPG(t *testing.T) {
	testEnvironmentBudgetReceipt(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func testEnvironmentBudgetReceipt(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@outcome.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "outcome-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "outcome-app", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	budgets := s.(state.EnvironmentRouteBudgetStore)
	receipts := s.(state.EnvironmentRouteBudgetReceiptStore)
	before, err := budgets.GetEnvironmentRouteBudgets(ctx, account.ID, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	req := api.SetEnvironmentRouteBudgetsRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &before.Revision, ExpectedAppRevision: &before.AppRevision, Enabled: true, Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/checkout", MaxP95MS: 300}}}
	committed, err := budgets.SetEnvironmentRouteBudgets(ctx, account.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := api.EnvironmentRouteBudgetRequestHash("staging", req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := receipts.GetEnvironmentRouteBudgetReceipt(ctx, account.ID, app.ID, "staging", req.RequestID)
	if err != nil || got.RequestHash != hash || got.RequestID != req.RequestID || got.AppID != app.ID || got.ProjectID != project.ID || got.EnvironmentID != env.ID || got.Scope != "staging" || got.Revision != committed.Revision || got.AppRevision != committed.AppRevision {
		t.Fatalf("receipt binding missing: %+v %v", got, err)
	}
	req2 := req
	req2.RequestID = uuid.NewString()
	req2.ExpectedRevision = &committed.Revision
	req2.Enabled = false
	req2.Routes = []api.RouteMonitorRoute{}
	later, err := budgets.SetEnvironmentRouteBudgets(ctx, account.ID, app.ID, "staging", req2)
	if err != nil {
		t.Fatal(err)
	}
	original, err := receipts.GetEnvironmentRouteBudgetReceipt(ctx, account.ID, app.ID, "staging", req.RequestID)
	if err != nil || !reflect.DeepEqual(got, original) {
		t.Fatal("later policy edits replaced original receipt")
	}
	unchanged, err := budgets.GetEnvironmentRouteBudgets(ctx, account.ID, app.ID, "staging")
	if err != nil || unchanged.Revision != later.Revision {
		t.Fatal("lookup changed policy")
	}
	for _, args := range [][4]string{{account.ID, app.ID, "default", req.RequestID}, {account.ID, app.ID, "staging", uuid.NewString()}, {uuid.NewString(), app.ID, "staging", req.RequestID}, {account.ID, uuid.NewString(), "staging", req.RequestID}} {
		if _, err := receipts.GetEnvironmentRouteBudgetReceipt(ctx, args[0], args[1], args[2], args[3]); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign/missing receipt leaked: %v", err)
		}
	}
	if _, err := receipts.GetEnvironmentRouteBudgetReceipt(ctx, account.ID, app.ID, "staging", "bad"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("invalid request UUID accepted")
	}
}
