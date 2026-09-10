// Account-wide deployment history must follow the same live-app visibility
// rule as the app list. Soft-deleted apps retain child rows for slug reuse and
// auditability, but those deployments must not leak into customer history.
//
//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_ListDeploymentsForAccount_ExcludesSoftDeletedApps(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx, "soft-delete-deployments-")

	rows, err := s.ListDeploymentsForAccount(ctx, acctID, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListDeploymentsForAccount before delete: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListDeploymentsForAccount before delete = %d rows, want 1", len(rows))
	}

	if _, err := s.SoftDeleteAppCascade(ctx, appID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}

	rows, err = s.ListDeploymentsForAccount(ctx, acctID, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListDeploymentsForAccount after delete: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListDeploymentsForAccount after delete = %d rows, want 0", len(rows))
	}

	// Exercise the cursor branch as well; the status predicate must be applied
	// consistently to both pagination forms.
	rows, err = s.ListDeploymentsForAccount(ctx, acctID, time.Now().UTC().Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("ListDeploymentsForAccount cursor after delete: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListDeploymentsForAccount cursor after delete = %d rows, want 0", len(rows))
	}
}

func TestPg_ListLatestDeploymentPerApp_IsScopedAndStable(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "latest-pg-owned@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateAccount(ctx, "latest-pg-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	createApp := func(accountID, slug string) state.App {
		t.Helper()
		app, createErr := s.CreateApp(ctx, state.App{
			AccountID: accountID, Slug: slug, Type: state.AppTypeApp,
			RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return app
	}
	createDeployment := func(appID, digest string) state.Deployment {
		t.Helper()
		deployment, createErr := s.CreateDeployment(ctx, state.Deployment{
			AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: digest,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return deployment
	}

	stamp := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	appA := createApp(account.ID, "latest-pg-a")
	firstA := createDeployment(appA.ID, "sha256:a1")
	secondA := createDeployment(appA.ID, "sha256:a2")
	if _, err := pool.Exec(ctx, `update deployments set created_at = $1 where id in ($2, $3)`, stamp, firstA.ID, secondA.ID); err != nil {
		t.Fatal(err)
	}
	wantA := firstA.ID
	if secondA.ID > wantA {
		wantA = secondA.ID
	}

	appB := createApp(account.ID, "latest-pg-b")
	wantB := createDeployment(appB.ID, "sha256:b")
	deleted := createApp(account.ID, "latest-pg-deleted")
	createDeployment(deleted.ID, "sha256:deleted")
	if err := s.DeleteApp(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}
	foreignApp := createApp(foreign.ID, "latest-pg-foreign")
	createDeployment(foreignApp.ID, "sha256:foreign")

	got, err := s.ListLatestDeploymentPerApp(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("latest map = %d rows, want 2: %+v", len(got), got)
	}
	if got[appA.ID].ID != wantA {
		t.Errorf("app A latest = %q, want tie-break winner %q", got[appA.ID].ID, wantA)
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
