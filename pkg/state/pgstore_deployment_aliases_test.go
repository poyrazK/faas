package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgDeploymentAliasesPinAppScopedDeployment(t *testing.T) {
	store, ctx := pgStore(t)
	suffix := uuid.NewString()
	account, err := store.CreateAccount(ctx, "deployment-aliases-"+suffix+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "alias-" + suffix[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployBuilding, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := store.SetDeploymentAlias(ctx, app.ID, "candidate", deployment.ID)
	if err != nil {
		t.Fatalf("SetDeploymentAlias: %v", err)
	}
	if created.DeploymentID != deployment.ID || created.Revision != deployment.Revision {
		t.Fatalf("created alias = %+v", created)
	}
	rows, err := store.ListDeploymentAliases(ctx, app.ID)
	if err != nil || len(rows) != 1 || rows[0].DeploymentID != deployment.ID {
		t.Fatalf("ListDeploymentAliases = %+v, %v", rows, err)
	}
	if err := store.DeleteDeploymentAlias(ctx, app.ID, "candidate"); err != nil {
		t.Fatalf("DeleteDeploymentAlias: %v", err)
	}
	if err := store.DeleteDeploymentAlias(ctx, app.ID, "candidate"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing DeleteDeploymentAlias = %v, want ErrNotFound", err)
	}
}
