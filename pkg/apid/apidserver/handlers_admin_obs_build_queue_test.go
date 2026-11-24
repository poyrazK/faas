package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The build queue report lists queued and running builds and the window's
// outcomes by kind and failure class (ADR-20261123163340). The test server
// has no Prometheus, so slot use is reported not_configured.
func TestObsBuildQueueReport(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	store := *e.Store()
	build := func(slug string, kind state.DeploymentKind) state.Build {
		t.Helper()
		a, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		d, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: a.ID, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		b, err := store.CreateBuild(ctx, d.ID, kind, 1024, "")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	set := func(b state.Build, status state.BuildStatus, fc state.FailureClass, started, finished bool) {
		t.Helper()
		if err := store.UpdateBuildStatus(ctx, b.ID, status, fc, started, finished); err != nil {
			t.Fatal(err)
		}
	}
	build("bq-waiting", state.DeploymentKindTarball)
	set(build("bq-running", state.DeploymentKindDockerfile), state.BuildRunning, "", true, false)
	ok := build("bq-ok", state.DeploymentKindTarball)
	set(ok, state.BuildRunning, "", true, false)
	set(ok, state.BuildSucceeded, "", false, true)
	oom := build("bq-oom", state.DeploymentKindTarball)
	set(oom, state.BuildRunning, "", true, false)
	set(oom, state.BuildFailed, state.FailureOOM, false, true)

	rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/build-queue?window=1h", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var r api.ObsBuildQueueReport
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.WindowSeconds != 3600 || r.SlotsState != "not_configured" || r.Totals.Queued != 1 || r.Totals.Running != 1 {
		t.Fatalf("report = %+v", r)
	}
	if r.Totals.Finished != 2 || r.Totals.Succeeded != 1 || r.Totals.Failed != 1 || len(r.Failures) != 1 || r.Failures[0].Class != "oom" {
		t.Fatalf("finished = %+v failures=%+v", r.Totals, r.Failures)
	}
	if len(r.Queued) != 1 || r.Queued[0].AppSlug != "bq-waiting" || len(r.Running) != 1 || r.Running[0].Kind != "dockerfile" {
		t.Fatalf("queued=%+v running=%+v", r.Queued, r.Running)
	}
	if len(r.RecentFailures) != 1 || r.RecentFailures[0].AppSlug != "bq-oom" || len(r.Nodes) != 1 || r.Nodes[0].Node != "unrecorded" {
		t.Fatalf("failures=%+v nodes=%+v", r.RecentFailures, r.Nodes)
	}

	if rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/build-queue?window=30d", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window: status %d", rec.Code)
	}
}
