package apidserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// boa multi-service deploy (2026-11-21): boa-contacts' v3 failed while v1
// kept serving. The reconcile had already recorded v3's source hash, so every
// later `deploy --project --only boa-contacts` reported it "unchanged" and
// queued nothing; only `release retry` rebuilt it. An app whose newest
// production deployment failed behind an older live release rebuilds; one
// whose newest deployment is the live one, or was cancelled by the user, or
// failed in another scope, does not.
func TestProductionAppsBehindFailedDeployment(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "boa@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	step := 0
	mk := func(slug string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	deploy := func(app state.App, scope string) state.Deployment {
		step++
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball,
			Status: state.DeployPending, Scope: scope, CreatedAt: base.Add(time.Duration(step) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return dep
	}
	live := func(app state.App) {
		if err := store.MarkDeploymentLive(ctx, deploy(app, "").ID); err != nil {
			t.Fatal(err)
		}
	}
	fail := func(app state.App, scope string) {
		if _, err := store.SetDeploymentFailed(ctx, deploy(app, scope).ID, "internal", "snapshot prime exceeded the scheduler deadline"); err != nil {
			t.Fatal(err)
		}
	}
	contacts, users, ledger, staging, history := mk("boa-contacts"), mk("boa-users"), mk("boa-ledger"), mk("boa-staging"), mk("boa-history")
	live(contacts)
	fail(contacts, "")
	live(users)
	live(ledger)
	if _, _, err := store.CancelDeploymentTx(ctx, deploy(ledger, "").ID, "user-1", state.CancelReasonUser); err != nil {
		t.Fatal(err)
	}
	live(staging)
	fail(staging, "staging")
	fail(history, "")
	live(history)

	got, err := apidserver.ProductionAppsWithoutRelease(ctx, store,
		[]state.App{contacts, users, ledger, staging, history}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != contacts.ID {
		slugs := make([]string, 0, len(got))
		for _, app := range got {
			slugs = append(slugs, app.Slug)
		}
		t.Fatalf("rebuilds = %v, want only %s", slugs, contacts.Slug)
	}

	// An app the reconcile already queued is not returned twice.
	got, err = apidserver.ProductionAppsWithoutRelease(ctx, store, []state.App{contacts}, nil, []state.App{contacts})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("queued app returned again: %+v", got)
	}
}
