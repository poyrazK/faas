package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreListLatestDeploymentPerApp(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "latest-owned@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(ctx, "latest-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	createApp := func(accountID, slug string) App {
		t.Helper()
		app, createErr := store.CreateApp(ctx, App{
			AccountID: accountID, Slug: slug, Type: AppTypeApp, Status: AppActive,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return app
	}
	createDeployment := func(appID, id string, createdAt time.Time) Deployment {
		t.Helper()
		deployment, createErr := store.CreateDeployment(ctx, Deployment{
			ID: id, AppID: appID, ImageDigest: "sha256:" + id,
			Kind: DeploymentKindImage, Status: DeployLive, CreatedAt: createdAt,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return deployment
	}

	stamp := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	appA := createApp(account.ID, "latest-store-a")
	lowID := "00000000-0000-0000-0000-000000000001"
	highID := "00000000-0000-0000-0000-000000000002"
	createDeployment(appA.ID, lowID, stamp)
	wantA := createDeployment(appA.ID, highID, stamp)

	appB := createApp(account.ID, "latest-store-b")
	wantB := createDeployment(appB.ID, "00000000-0000-0000-0000-000000000003", stamp.Add(time.Minute))

	deleted := createApp(account.ID, "latest-store-deleted")
	createDeployment(deleted.ID, "00000000-0000-0000-0000-000000000004", stamp.Add(2*time.Minute))
	if err := store.DeleteApp(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}

	foreignApp := createApp(foreign.ID, "latest-store-foreign")
	createDeployment(foreignApp.ID, "00000000-0000-0000-0000-000000000005", stamp.Add(3*time.Minute))

	got, err := store.ListLatestDeploymentPerApp(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("latest map = %d rows, want 2: %+v", len(got), got)
	}
	if got[appA.ID].ID != wantA.ID {
		t.Errorf("app A latest = %q, want tie-break winner %q", got[appA.ID].ID, wantA.ID)
	}
	if got[appB.ID].ID != wantB.ID {
		t.Errorf("app B latest = %q, want %q", got[appB.ID].ID, wantB.ID)
	}
	if _, ok := got[deleted.ID]; ok {
		t.Error("soft-deleted app leaked into result")
	}
	if _, ok := got[foreignApp.ID]; ok {
		t.Error("foreign app leaked into result")
	}
}

func TestMemStoreListLatestDeploymentPerAppEmpty(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "latest-empty@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.ListLatestDeploymentPerApp(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("latest map = %#v, want non-nil empty map", got)
	}
}
