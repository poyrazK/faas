//go:build !no_pg

// adr: 211
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectEnvironmentClonePreservesSecretVersion(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "project-secret-version@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "secret-version", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "secret-version-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"1111111111111111", "2222222222222222"} {
		if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "production", "TOKEN", "age1", hash, []byte("sealed")); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err = store.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging",
	}, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := store.GetAppSecretInScope(ctx, account.ID, app.ID, "staging", "TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if cloned.SecretVersion != 2 || cloned.DeliveryVersion != 1 || cloned.DeliveredVersion != 0 || cloned.DeliveryStatus != state.SecretDeliveryPending {
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
