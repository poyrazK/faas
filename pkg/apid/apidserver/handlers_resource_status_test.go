package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The status subresources return conditions derived from existing evidence
// (ADR-20261030151810): an idle app is ready without being woken, a failed
// deployment is terminal, and another account's resources are not found.
func TestResourceStatusAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "status-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	// A live deployment that finished its readiness stage, as one is after a
	// deploy; until then it is routable but reports verifying.
	readiness := []byte(`{"current":"live","history":[{"name":"readiness","status":"completed"}]}`)
	live, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100, TrafficPercentExplicit: true, RootfsPath: "/artifact", StageState: readiness})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployFailed, Error: "build exited 1"})
	if err != nil {
		t.Fatal(err)
	}

	appStatus := readStatus(t, e, "/v1/apps/status-app/status")
	if appStatus.Kind != api.ResourceKindApp || appStatus.Name != "status-app" || appStatus.ObservedAt == "" {
		t.Fatalf("app status header = %+v", appStatus)
	}
	if ready := condition(t, appStatus, api.ConditionReady); ready.Status != api.ConditionTrue || ready.Reason != "idle" {
		t.Errorf("idle app Ready = %+v, want True/idle", ready)
	}
	instances, err := (*e.Store()).ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(instances) != 0 {
		t.Fatal("a status read must not wake an idle app")
	}

	if ready := condition(t, readStatus(t, e, "/v1/deployments/"+live.ID+"/status"), api.ConditionReady); ready.Status != api.ConditionTrue {
		t.Errorf("live deployment Ready = %+v", ready)
	}
	ready := condition(t, readStatus(t, e, "/v1/deployments/"+failed.ID+"/status"), api.ConditionReady)
	if ready.Status != api.ConditionFalse || ready.Reason != "failed" || !ready.Terminal || ready.Message != "The deployment failed: build exited 1" {
		t.Errorf("failed deployment Ready = %+v", ready)
	}

	other, err := (*e.Store()).CreateAccount(t.Context(), "other-status@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherAppID := apidserver.MustSeedAppFor(t, (*e.Store()), other.ID, "private-status")
	otherDeployment, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: otherAppID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/apps/private-status/status", "/v1/apps/missing/status", "/v1/deployments/" + otherDeployment.ID + "/status"} {
		if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

// Domains, crons and job runs read their conditions from durable rows: a
// verified domain with an issued certificate serves, a cron that never ran
// says so, a queued run is progressing, and a run under another job's name
// is not found.
func TestResourceStatusAPI_DomainCronJobRun(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "status-kinds", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.CreateCustomDomain(ctx, "shop.example.com", app.ID, "tok"); err != nil {
		t.Fatal(err)
	}
	domain := readStatus(t, e, "/v1/domains/shop.example.com/status")
	if ready := condition(t, domain, api.ConditionReady); ready.Status != api.ConditionFalse || ready.Reason != api.ConditionReasonDNSPending {
		t.Errorf("unverified domain Ready = %+v", ready)
	}
	if err := store.MarkDomainVerified(ctx, "shop.example.com"); err != nil {
		t.Fatal(err)
	}
	if verified := condition(t, readStatus(t, e, "/v1/domains/SHOP.example.com/status"), api.ConditionVerified); verified.Status != api.ConditionTrue || verified.Since == "" {
		t.Errorf("verified domain Verified = %+v", verified)
	}

	cron, err := store.CreateCron(ctx, app.ID, "*/5 * * * *", "/tick", true)
	if err != nil {
		t.Fatal(err)
	}
	cronStatus := readStatus(t, e, "/v1/crons/"+cron.ID+"/status")
	if ready := condition(t, cronStatus, api.ConditionReady); ready.Status != api.ConditionTrue || ready.Reason != api.ConditionReasonScheduled {
		t.Errorf("cron Ready = %+v", ready)
	}
	if last := condition(t, cronStatus, api.ConditionLastRunSucceeded); last.Status != api.ConditionUnknown || last.Reason != api.ConditionReasonNeverRun {
		t.Errorf("cron LastRunSucceeded = %+v", last)
	}

	job, err := store.JobCreate(ctx, (*e.Acct()).ID, "status-job", "app", "", nil, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, (*e.Acct()).ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	runStatus := readStatus(t, e, "/v1/jobs/status-job/runs/"+run.ID+"/status")
	if runStatus.Kind != api.ResourceKindJobRun || runStatus.Name != run.ID {
		t.Fatalf("job run status header = %+v", runStatus)
	}
	if progressing := condition(t, runStatus, api.ConditionProgressing); progressing.Status != api.ConditionTrue {
		t.Errorf("queued run Progressing = %+v", progressing)
	}
	if succeeded := condition(t, runStatus, api.ConditionSucceeded); succeeded.Status != api.ConditionFalse || succeeded.Terminal {
		t.Errorf("queued run Succeeded = %+v", succeeded)
	}

	for _, path := range []string{"/v1/domains/missing.example.com/status", "/v1/crons/00000000-0000-0000-0000-000000000000/status", "/v1/jobs/other-job/runs/" + run.ID + "/status"} {
		if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestResourceStatusAPI_ReadScopeRequired(t *testing.T) {
	e := apidserver.SetupWithScopes(t, []string{api.ScopeDeployWrite})
	apidserver.MustSeedApp(t, e, "status-app")
	if rec := e.Do(t, http.MethodGet, "/v1/apps/status-app/status", nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func readStatus(t *testing.T, e apidserver.TestEnv, path string) api.ResourceStatus {
	t.Helper()
	rec := e.Do(t, http.MethodGet, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("%s: conditions must not be cached", path)
	}
	var got api.ResourceStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func condition(t *testing.T, s api.ResourceStatus, typ string) api.Condition {
	t.Helper()
	for _, c := range s.Conditions {
		if c.Type == typ {
			return c
		}
	}
	t.Fatalf("%s has no %s condition: %+v", s.Name, typ, s.Conditions)
	return api.Condition{}
}
