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

func TestCloneProjectEnvironmentCarriesUniquePRPreviewIdentity(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "clone-preview-identity@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	created, _, err := store.CloneProjectEnvironment(ctx, ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "pr-381",
		PreviewPRNumber: 381, PreviewHeadSHA: sha,
	}, api.Limits{SecretCountMax: 10, EnvVarsMax: 10})
	if err != nil {
		t.Fatal(err)
	}
	if created.PreviewPRNumber != 381 || created.PreviewHeadSHA != sha {
		t.Fatalf("created preview identity = PR %d SHA %q", created.PreviewPRNumber, created.PreviewHeadSHA)
	}
	byPR, err := store.ProjectEnvironmentByPreviewPR(ctx, account.ID, project.ID, 381)
	if err != nil || byPR.ID != created.ID {
		t.Fatalf("environment by preview PR = %+v err=%v", byPR, err)
	}
	if _, _, err := store.CloneProjectEnvironment(ctx, ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "pr-381-copy",
		PreviewPRNumber: 381, PreviewHeadSHA: sha,
	}, api.Limits{SecretCountMax: 10, EnvVarsMax: 10}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate PR preview clone err = %v, want ErrConflict", err)
	}
	if _, _, err := store.CloneProjectEnvironment(ctx, ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "protected-pr",
		TargetProtected: true, PreviewPRNumber: 382, PreviewHeadSHA: sha,
	}, api.Limits{SecretCountMax: 10, EnvVarsMax: 10}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("protected PR preview clone err = %v, want ErrInvalidArgument", err)
	}
}

func TestCloneProjectEnvironmentDoesNotClaimSharedOpenAPIRoutes(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "shared-openapi-routes@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "shop", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{
		AccountID: account.ID, ProjectID: project.ID, Slug: "shop-api", Status: AppActive,
		OnlyAllowDeclaredRoutes: true, // An app-wide OpenAPI document supplies the routes.
	})
	if err != nil {
		t.Fatal(err)
	}
	_, result, err := store.CloneProjectEnvironment(ctx, ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging",
	}, api.Limits{SecretCountMax: 10, EnvVarsMax: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.RoutesCopied != 0 || len(result.SharedResources) != 2 || result.SharedResources[0] != "routes" || result.SharedResources[1] != "policies" {
		t.Fatalf("clone claimed shared OpenAPI routes: %+v", result)
	}
	if _, err := store.GetProjectEnvironmentRoutePolicy(ctx, account.ID, app.ID, "staging"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsnapshotted route policy = %v, want ErrNotFound", err)
	}
}

func TestCloneProjectEnvironmentPreservesKnownSecretVersionButNotDelivery(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "clone-secret-version@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "shop", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-api", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"1111111111111111", "2222222222222222"} {
		if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "production", "TOKEN", "age1", hash, []byte("sealed")); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err = store.CloneProjectEnvironment(ctx, ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging",
	}, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := store.GetAppSecretInScope(ctx, account.ID, app.ID, "staging", "TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if cloned.SecretVersion != 2 || cloned.DeliveryVersion != 1 || cloned.DeliveredVersion != 0 || cloned.DeliveryStatus != SecretDeliveryPending {
		t.Fatalf("cloned secret metadata = %+v, want known revision 2 and fresh pending delivery", cloned)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "staging", "TOKEN", "age1", "3333333333333333", []byte("sealed-new")); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetAppSecretInScope(ctx, account.ID, app.ID, "staging", "TOKEN")
	if err != nil || updated.SecretVersion != 3 {
		t.Fatalf("updated clone version = %+v err=%v, want 3", updated, err)
	}
	source, err := store.GetAppSecretInScope(ctx, account.ID, app.ID, "production", "TOKEN")
	if err != nil || source.SecretVersion != 2 {
		t.Fatalf("source version changed = %+v err=%v", source, err)
	}
}
