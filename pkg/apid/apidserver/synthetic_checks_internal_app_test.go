// adr: 20261031144636 — synthetic checks probe the public hostname, which an
// internal app does not serve: creating or resuming a check on one is refused.
package apidserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSyntheticChecksRefuseInternalApps(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	app, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "reports-internal", Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassHTTP, Visibility: api.AppVisibilityInternal})
	if err != nil {
		t.Fatal(err)
	}
	req := api.CreateSyntheticCheckRequest{Name: "health", Path: "/healthz", IntervalSeconds: 300}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, "/v1/apps/reports-internal/synthetics", req, nil), http.StatusConflict, api.CodeSyntheticCheckInvalid)

	// A check made while the app was public is not resumed while it is internal.
	check, err := store.CreateSyntheticCheck(context.Background(), state.SyntheticCheck{AccountID: acct.ID, AppID: app.ID, Name: "old", Method: "GET",
		Path: "/", TimeoutMS: 5000, IntervalSeconds: 300}, api.MaxSyntheticChecksPerApp)
	if err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPatch, "/v1/apps/reports-internal/synthetics/"+check.ID, api.UpdateSyntheticCheckRequest{Enabled: true}, nil),
		http.StatusConflict, api.CodeSyntheticCheckInvalid)
	if rec := e.Do(t, http.MethodPatch, "/v1/apps/reports-internal/synthetics/"+check.ID, api.UpdateSyntheticCheckRequest{Enabled: false}, nil); rec.Code != http.StatusOK {
		t.Fatalf("pausing a check on an internal app = %d %s; want 200", rec.Code, rec.Body)
	}
	runnable, err := store.ListRunnableSyntheticChecks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range runnable {
		if c.AppID == app.ID {
			t.Fatalf("the runner lists a check on an internal app: %+v", c)
		}
	}
}
