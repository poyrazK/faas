package state

// adr: 478

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func releaseTestApp(t *testing.T, store Store, account Account) App {
	t.Helper()
	app, err := store.CreateApp(t.Context(), App{ID: uuid.NewString(), AccountID: account.ID,
		Slug: "release-" + uuid.NewString(), Type: AppTypeApp, Status: AppActive, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func releaseTestIntent(cellID string, app App) (RegionalImageReleaseIntent, Deployment) {
	image := "ghcr.io/gregale/shop@sha256:" + strings.Repeat("a", 64)
	return RegionalImageReleaseIntent{CellID: cellID, AppID: app.ID, ReleaseID: uuid.NewString(), AccountID: app.AccountID, OrgID: app.OrgID, ImageRef: image},
		Deployment{AppID: app.ID, ImageDigest: image, Kind: DeploymentKindImage}
}

func releaseTestActivity(app App) *OrgActivity {
	actor, appID := uuid.MustParse(app.AccountID), uuid.MustParse(app.ID)
	orgID := uuid.New()
	if app.OrgID != "" {
		orgID = uuid.MustParse(app.OrgID)
	}
	return &OrgActivity{OrgID: orgID, Kind: "deploy.requested", ActorType: OrgActivityActorUser, ActorAccountID: &actor,
		ActorLabel: "person@example.com", ResourceType: "deployment", ResourceID: app.ID, ResourceLabel: app.Slug,
		AppID: &appID, SourceType: "deployment.requested", SourceID: "release-test", Data: []byte(`{}`)}
}

func runRegionalImageReleaseContract(t *testing.T, store Store, releases RegionalImageReleaseStore, cellID string, account Account) {
	t.Helper()
	t.Run("concurrent-lost-response", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		intent, dep := releaseTestIntent(cellID, app)
		const workers = 16
		activity := releaseTestActivity(app)
		results := make(chan RegionalImageReleaseResult, workers)
		errorsCh := make(chan error, workers)
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				result, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, activity)
				results <- result
				errorsCh <- err
			})
		}
		wg.Wait()
		close(results)
		close(errorsCh)
		for err := range errorsCh {
			if err != nil {
				t.Fatal(err)
			}
		}
		id, accepted := "", 0
		for result := range results {
			if id == "" {
				id = result.Receipt.DeploymentID
			}
			if result.Receipt.DeploymentID != id {
				t.Fatal("concurrent retry inserted another deployment")
			}
			if !result.Replayed {
				accepted++
				if result.ActivityOutboxID == 0 {
					t.Fatal("acceptance omitted activity event")
				}
			} else if result.ActivityOutboxID != 0 {
				t.Fatal("replay produced another activity event")
			}
		}
		if accepted != 1 {
			t.Fatalf("accepted %d times", accepted)
		}
		rows, err := store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
		if err != nil || len(rows) != 1 {
			t.Fatalf("deployment count: %d %v", len(rows), err)
		}
	})
	t.Run("retry-does-not-supersede-newer-release", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		intent, dep := releaseTestIntent(cellID, app)
		first, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
		if err != nil {
			t.Fatal(err)
		}
		newer, err := store.CreateDeployment(t.Context(), dep)
		if err != nil {
			t.Fatal(err)
		}
		retry, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
		if err != nil || !retry.Replayed || retry.Receipt.DeploymentID != first.Receipt.DeploymentID || retry.Receipt.DeploymentStatus != string(DeploySuperseded) {
			t.Fatalf("retry: %+v %v", retry, err)
		}
		current, err := store.DeploymentByID(t.Context(), newer.ID)
		if err != nil || current.Status != DeployPending {
			t.Fatalf("newer deployment changed: %+v %v", current, err)
		}
	})
	t.Run("failed-release-is-not-resubmitted", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		intent, dep := releaseTestIntent(cellID, app)
		first, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), first.Receipt.DeploymentID, DeployFailed, "test failure"); err != nil {
			t.Fatal(err)
		}
		retry, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
		if err != nil || !retry.Replayed || retry.Receipt.DeploymentStatus != string(DeployFailed) || retry.Receipt.DeploymentID != first.Receipt.DeploymentID {
			t.Fatalf("failed retry: %+v %v", retry, err)
		}
	})

	t.Run("conflicting-image", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		intent, dep := releaseTestIntent(cellID, app)
		if _, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil); err != nil {
			t.Fatal(err)
		}
		intent.ImageRef = strings.ReplaceAll(intent.ImageRef, strings.Repeat("a", 64), strings.Repeat("b", 64))
		dep.ImageDigest = intent.ImageRef
		if _, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrRegionalImageReleaseConflict) {
			t.Fatalf("conflict: %v", err)
		}
		rows, err := store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
		if err != nil || len(rows) != 1 || rows[0].Status != DeployPending {
			t.Fatalf("conflict mutated intent: %+v %v", rows, err)
		}
	})
	t.Run("pinned-owner-and-org", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		for _, field := range []string{"owner", "org"} {
			intent, dep := releaseTestIntent(cellID, app)
			if field == "owner" {
				intent.AccountID = uuid.NewString()
			} else {
				intent.OrgID = uuid.NewString()
			}
			if _, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrNotFound) {
				t.Fatalf("changed %s: %v", field, err)
			}
			if _, err := releases.RegionalImageReleaseReceipt(t.Context(), cellID, app.ID, intent.ReleaseID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("rejected mutation created receipt: %v", err)
			}
		}
	})
	t.Run("deleted-app-cannot-replay", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		intent, dep := releaseTestIntent(cellID, app)
		if _, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteApp(t.Context(), app.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted app replay: %v", err)
		}
	})
	t.Run("invalid-intent", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		intent, dep := releaseTestIntent(cellID, app)
		for _, field := range []string{"release", "cell", "image", "kind", "scope"} {
			i, d := intent, dep
			switch field {
			case "release":
				i.ReleaseID = uuid.Nil.String()
			case "cell":
				i.CellID = strings.ReplaceAll(i.CellID, "-", "")
			case "image":
				i.ImageRef = "shop:latest"
				d.ImageDigest = i.ImageRef
			case "kind":
				d.Kind = DeploymentKindTarball
			case "scope":
				d.Scope = "preview"
			}
			if _, err := releases.CreateRegionalImageRelease(t.Context(), i, d, nil); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid %s: %v", field, err)
			}
		}
	})
}

func TestMemRegionalImageReleaseContract(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(t.Context(), "release@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runRegionalImageReleaseContract(t, store, store, uuid.NewString(), account)
}

func TestMemRegionalImageReleaseTombstone(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(t.Context(), "collected@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app := releaseTestApp(t, store, account)
	intent, dep := releaseTestIntent(uuid.NewString(), app)
	first, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	delete(store.deployments, first.Receipt.DeploymentID)
	store.mu.Unlock()
	receipt, err := store.RegionalImageReleaseReceipt(t.Context(), intent.CellID, app.ID, intent.ReleaseID)
	if err != nil || receipt.DeploymentID != "" || receipt.DeploymentStatus != "expired" {
		t.Fatalf("tombstone: %+v %v", receipt, err)
	}
	if _, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrRegionalImageReleaseExpired) {
		t.Fatalf("collected retry: %v", err)
	}
	rows, err := store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("resurrected collected release: %+v %v", rows, err)
	}
}

func TestMemRegionalImageReleaseOwnerChange(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(t.Context(), "old-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app := releaseTestApp(t, store, account)
	intent, dep := releaseTestIntent(uuid.NewString(), app)
	if _, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil); err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAccount(t.Context(), "new-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	app.AccountID, app.OrgID = owner.ID, ""
	store.apps[app.ID] = app
	store.mu.Unlock()
	if _, err := store.RegionalImageReleaseReceipt(t.Context(), intent.CellID, app.ID, intent.ReleaseID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("previous owner's receipt disclosed: %v", err)
	}
	if _, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale owner accepted: %v", err)
	}
	intent.AccountID, intent.OrgID = owner.ID, ""
	if _, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new owner rebound previous receipt: %v", err)
	}
}
