// adr: 211
package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCloneProjectEnvironmentQuotaFailureLeavesNoTarget(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "clone-quota@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "shop", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-api", WorkloadName: "api", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "production", "TOKEN", "age1", "1111111111111111", []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.CloneProjectEnvironment(ctx, ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging",
	}, api.Limits{SecretCountMax: 1, EnvVarsMax: 10})
	var quota *ProjectEnvironmentCloneQuotaError
	if !errors.As(err, &quota) || quota.Resource != "secrets" || quota.Observed != 2 {
		t.Fatalf("clone error=%v quota=%+v", err, quota)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, "staging"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("quota failure created target: %v", err)
	}
}
