package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectServiceAliasesStayWithinCreatorAndProject(t *testing.T) {
	m := state.NewMemStore()
	a, err := m.CreateAccount(t.Context(), "aliases@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := m.CreateAccount(t.Context(), "other@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	global, err := m.CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "api", WorkloadName: "api"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := m.CreateApp(t.Context(), state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "shop-backend", WorkloadName: "api"})
	if err != nil {
		t.Fatal(err)
	}
	caller := state.App{AccountID: a.ID, ProjectID: p.ID}
	resolved, err := state.AppByServiceName(t.Context(), m, caller, "api")
	if err != nil || resolved.ID != target.ID {
		t.Fatalf("resolved outside project: %+v %v", resolved, err)
	}
	resolved, err = state.AppByServiceName(t.Context(), m, state.App{AccountID: a.ID}, "api")
	if err != nil || resolved.ID != global.ID {
		t.Fatalf("standalone resolution changed: %+v %v", resolved, err)
	}
}
