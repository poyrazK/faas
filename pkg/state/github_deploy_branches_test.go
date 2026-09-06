package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreProjectDeployBranchesReplaceAndScope(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "branches@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "repo"})
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]string{"main": "production", "staging": "staging"}
	if err := m.ReplaceProjectDeployBranches(ctx, account.ID, project.ID, rules); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListProjectDeployBranches(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got["main"] != "production" || got["staging"] != "staging" {
		t.Fatalf("rules = %#v", got)
	}
	// The store owns its copy; mutating the caller's map cannot alter state.
	rules["staging"] = "changed"
	got, _ = m.ListProjectDeployBranches(ctx, project.ID)
	if got["staging"] != "staging" {
		t.Fatalf("stored rules changed through caller map: %#v", got)
	}
	if err := m.ReplaceProjectDeployBranches(ctx, account.ID, project.ID, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	got, _ = m.ListProjectDeployBranches(ctx, project.ID)
	if len(got) != 0 {
		t.Fatalf("cleared rules = %#v", got)
	}
	if _, err := m.ListProjectDeployBranches(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project error = %v", err)
	}
}

func TestValidateDeployBranchMappingRejectsInvalidScope(t *testing.T) {
	if err := validateDeployBranchMapping(map[string]string{"staging": "not valid"}); err == nil {
		t.Fatal("invalid scope accepted")
	}
}

func TestMemStoreDeploymentsDoNotSupersedeAcrossScopes(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)
	staging, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	production, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production"})
	if err != nil {
		t.Fatal(err)
	}
	gotStaging, _ := m.DeploymentByID(ctx, staging.ID)
	gotProduction, _ := m.DeploymentByID(ctx, production.ID)
	if gotStaging.Status == DeploySuperseded || gotProduction.Status == DeploySuperseded {
		t.Fatalf("cross-scope supersede: staging=%q production=%q", gotStaging.Status, gotProduction.Status)
	}
}
