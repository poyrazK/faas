package apidserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestOneTenantsDeploymentFailuresDoNotDegradePublicStatus is the Hunt 14
// H14-22 regression: one tenant's apps that kept failing readiness put the
// public page at 88.89% "deployments ok" and degraded. The percentage stays
// honest, but degrading the platform needs failures from at least two
// accounts.
func TestOneTenantsDeploymentFailuresDoNotDegradePublicStatus(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	newAccount := func(email string) state.Account {
		acct, err := store.CreateAccount(ctx, email, api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return acct
	}
	tenant, neighbor := newAccount("status-tenant@example.com"), newAccount("status-neighbor@example.com")
	newApp := func(acct state.Account, slug string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	live := func(app state.App) {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, CreatedAt: time.Now().Add(-time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
	}
	fail := func(app state.App) {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, CreatedAt: time.Now().Add(-time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetDeploymentFailed(ctx, dep.ID, api.CodeDeploymentSmokeFailed, "readiness never passed"); err != nil {
			t.Fatal(err)
		}
	}
	healthy := newApp(neighbor, "status-healthy")
	for range 14 {
		live(healthy)
	}
	fail(newApp(tenant, "status-slow"))
	fail(newApp(tenant, "status-grpc"))

	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"100"]}]}}`))
	}))
	defer prom.Close()

	page, err := apidserver.StatusPageFromStore(ctx, prom.URL, store)
	if err != nil {
		t.Fatal(err)
	}
	if page.BuildSuccessPct != 87.5 {
		t.Fatalf("deployments ok = %.2f%%, want 87.50%% (14 of 16)", page.BuildSuccessPct)
	}
	if page.Degraded {
		t.Fatalf("one tenant's failing apps degraded public status: source %q", page.Source)
	}

	fail(healthy)
	page, err = apidserver.StatusPageFromStore(ctx, prom.URL, store)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Degraded || !strings.Contains(page.Source, "deployment success target breached") {
		t.Fatalf("failures in two accounts: degraded = %v, source %q; want degraded by deployment success", page.Degraded, page.Source)
	}
}
