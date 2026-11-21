package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// prod 2026-11-20: `app exec` on a live function answered "the app needs a
// live deployment with a materialized runtime image". A function's release
// has no image to start a task VM from; the refusal now says so.
func TestAppTaskOnFunctionSaysFunctionsHaveNoImage(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	ctx := context.Background()
	app, err := (*e.Store()).CreateApp(ctx, state.App{
		AccountID: (*e.Acct()).ID, Slug: "task-function", Type: state.AppTypeFunction,
		Runtime: "node22", WorkloadClass: state.WorkloadClassHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), Kind: state.DeploymentKindTarball, AppID: app.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPost, "/v1/apps/task-function/tasks", api.CreateAppTaskRequest{Command: []string{"node", "-v"}}, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "a function's release has none") {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
}
