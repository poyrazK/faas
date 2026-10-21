package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreDeploymentAliasesPinImmutableDeployment(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "deployment-aliases@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "alias-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployBuilding, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, CreatedAt: time.Now().UTC().Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "other-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateDeployment(ctx, state.Deployment{AppID: otherApp.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}

	created, err := store.SetDeploymentAlias(ctx, app.ID, "candidate", first.ID)
	if err != nil {
		t.Fatalf("SetDeploymentAlias: %v", err)
	}
	if created.DeploymentID != first.ID || created.Revision != first.Revision || created.CreatedAt.IsZero() {
		t.Fatalf("created alias = %+v", created)
	}
	hostLabel, ok := api.DeploymentAliasHostLabel(app.ID, "candidate")
	if !ok {
		t.Fatal("host label rejected valid app alias")
	}
	routed, err := store.DeploymentAliasByHostLabel(ctx, hostLabel)
	if err != nil || routed.AppID != app.ID || routed.DeploymentID != first.ID {
		t.Fatalf("DeploymentAliasByHostLabel = %+v, %v", routed, err)
	}
	updated, err := store.SetDeploymentAlias(ctx, app.ID, "candidate", second.ID)
	if err != nil {
		t.Fatalf("update alias: %v", err)
	}
	if updated.DeploymentID != second.ID || updated.Revision != second.Revision || updated.CreatedAt != created.CreatedAt {
		t.Fatalf("updated alias = %+v, want target %s and stable creation time", updated, second.ID)
	}
	if _, err := store.SetDeploymentAlias(ctx, app.ID, "candidate", other.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-app target = %v, want ErrNotFound", err)
	}
	if _, err := store.SetDeploymentAlias(ctx, app.ID, "Bad_Name", first.ID); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid name = %v, want ErrInvalidArgument", err)
	}
	if _, err := store.SetDeploymentAlias(ctx, app.ID, strings.Repeat("a", 27), first.ID); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("too-long hostname alias = %v, want ErrInvalidArgument", err)
	}
	if _, err := store.SetDeploymentAlias(ctx, app.ID, "failed", "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing target = %v, want ErrNotFound", err)
	}
	rows, err := store.ListDeploymentAliases(ctx, app.ID)
	if err != nil || len(rows) != 1 || rows[0].DeploymentID != second.ID {
		t.Fatalf("ListDeploymentAliases = %+v, %v", rows, err)
	}
	if err := store.DeleteDeploymentAlias(ctx, app.ID, "candidate"); err != nil {
		t.Fatalf("DeleteDeploymentAlias: %v", err)
	}
	if _, err := store.DeploymentAliasByHostLabel(ctx, hostLabel); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted host lookup = %v, want ErrNotFound", err)
	}
	if err := store.DeleteDeploymentAlias(ctx, app.ID, "candidate"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second DeleteDeploymentAlias = %v, want ErrNotFound", err)
	}
}

func TestMemStoreDeploymentAliasRejectsExistingAppHostnameCollision(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "deployment-alias-host-conflict@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "orders-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	hostLabel, ok := api.DeploymentAliasHostLabel(app.ID, "x")
	if !ok {
		t.Fatal("host label rejected valid alias")
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: hostLabel, Status: state.AppActive}); err != nil {
		t.Fatalf("seed legacy app using reserved alias host: %v", err)
	}
	if _, err := store.SetDeploymentAlias(ctx, app.ID, "x", deployment.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("SetDeploymentAlias collision = %v, want ErrConflict", err)
	}
}
