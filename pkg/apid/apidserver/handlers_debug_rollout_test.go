package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 127 — debug status and rollout explanations are debugger reads: plan
// gated, app scoped, and well-formed when there is nothing to explain.
func TestDebugStatusAndRollout(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "status-debug-app", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	other, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "status-debug-other", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	otherDep, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: other.ID, Status: state.DeployPending, SourceSHA256: "x"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	// Request telemetry is Postgres-only; the success paths that read it are
	// in handlers_debug_rollout_pg_test.go. With no deployment there is
	// nothing to read.
	rec := e.Do(t, http.MethodGet, "/v1/apps/status-debug-app/debug/rollout", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollout status = %d, body = %s", rec.Code, rec.Body)
	}
	var rollout api.DebugRolloutExplanation
	if err := json.Unmarshal(rec.Body.Bytes(), &rollout); err != nil {
		t.Fatalf("decode rollout: %v", err)
	}
	if rollout.State != api.DebugRolloutNone || rollout.Routes == nil || rollout.Examples == nil || app.ID == "" {
		t.Fatalf("no-rollout explanation = %+v", rollout)
	}

	for _, tc := range []struct {
		path string
		code int
		want string
	}{
		{"/v1/apps/status-debug-app/debug/rollout?deployment_id=nope", http.StatusBadRequest, api.CodeValidation},
		{"/v1/apps/status-debug-app/debug/rollout?deployment_id=" + otherDep.ID, http.StatusNotFound, api.CodeNotFound},
		{"/v1/apps/status-debug-app/debug/status?since=forever", http.StatusBadRequest, api.CodeValidation},
	} {
		apidserver.AssertProblem(t, e.Do(t, http.MethodGet, tc.path, nil, nil), tc.code, tc.want)
	}
}

func TestDebugStatusAndRollout_ArePlanGated(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	if _, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "status-debug-free", Status: state.AppActive}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	for _, path := range []string{"/v1/apps/status-debug-free/debug/status", "/v1/apps/status-debug-free/debug/rollout"} {
		apidserver.AssertProblem(t, e.Do(t, http.MethodGet, path, nil, nil), http.StatusPaymentRequired, api.CodePlanFeatureGated)
	}
}
