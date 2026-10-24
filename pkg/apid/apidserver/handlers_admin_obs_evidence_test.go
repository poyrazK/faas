package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestObsIncidentEvidenceBoundedAndReadableAfterRecovery(t *testing.T) {
	e := gapEnv(t)
	ctx := context.Background()
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "evidence-app", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Status: state.DeployFailed, ErrorCode: api.CodeStageImageBuildOOM})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := (*e.Store()).AppendEventWithTrace(ctx, "ops@private.example", "operator.action.retry_deployment", &app.AccountID,
			[]byte(`{"deployment_id":"`+dep.ID+`","secret":"private-payload"}`), traceStringPtr(testOperatorTraceID)); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/admin/obs/incidents/deployment/" + dep.ID + "/evidence?limit=2"
	rec := e.DoOperator(t, http.MethodGet, path, nil, nil)
	var response api.ObsIncidentEvidenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(response.History) != 2 || !response.Truncated || response.ErrorCode != api.CodeStageImageBuildOOM || response.Incident.AppID != app.ID {
		t.Fatalf("unexpected evidence: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if response.History[0].TracePath != "/v1/admin/obs/traces/"+testOperatorTraceID || len(response.UnavailableSources) != 0 {
		t.Fatalf("missing trace path or false dependency: %+v", response)
	}
	if strings.Contains(rec.Body.String(), "private-payload") || strings.Contains(rec.Body.String(), "ops@private.example") {
		t.Fatal("evidence leaked raw payload or actor identity")
	}
	if err := (*e.Store()).UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	rec = e.DoOperator(t, http.MethodGet, path, nil, nil)
	response = api.ObsIncidentEvidenceResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || response.Incident.Status != "live" || response.Incident.Severity != "info" || response.ErrorCode != "" {
		t.Fatalf("recovered resource disappeared: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestObsIncidentEvidenceJobAndNodeCurrentState(t *testing.T) {
	e := gapEnv(t)
	ctx := context.Background()
	job, err := (*e.Store()).JobCreate(ctx, (*e.Acct()).ID, "evidence-job", "app", "", nil, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := (*e.Store()).JobRunCreate(ctx, job.ID, (*e.Acct()).ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).JobRunCancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	node, err := (*e.Store()).UpsertComputeNodeFromOperator(ctx, state.ComputeNode{ID: uuid.NewString(), Name: "evidence-node", Active: true, Lifecycle: state.NodeLifecycleActive, LastHeartbeatAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, id, status string }{{"job_run", run.ID, "cancelled"}, {"compute_node", node.ID, "active"}} {
		rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/incidents/"+tc.kind+"/"+tc.id+"/evidence", nil, nil)
		var response api.ObsIncidentEvidenceResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || response.Incident.Status != tc.status || response.History == nil || response.UnavailableSources == nil {
			t.Fatalf("%s status=%d body=%s", tc.kind, rec.Code, rec.Body.String())
		}
	}
}

func TestObsIncidentEvidenceValidationMissingAndUnavailable(t *testing.T) {
	e := gapEnv(t)
	for _, tc := range []struct {
		ref, query string
		want       int
	}{
		{"unknown/" + uuid.NewString(), "", 400},
		{"deployment/not-a-uuid", "", 400},
		{"deployment/" + uuid.NewString(), "?limit=0", 400},
		{"deployment/" + uuid.NewString(), "?limit=101", 400},
		{"deployment/" + uuid.NewString(), "", 404},
		{"job_run/" + uuid.NewString(), "", 404},
		{"compute_node/" + uuid.NewString(), "", 404},
		{"platform_alert/" + strings.Repeat("a", 64), "", 503},
	} {
		rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/incidents/"+tc.ref+"/evidence"+tc.query, nil, nil)
		if rec.Code != tc.want {
			t.Errorf("%s%s: status=%d want=%d body=%s", tc.ref, tc.query, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestObsIncidentEvidenceRequiresAdmin(t *testing.T) {
	for _, tc := range []struct {
		scopes []string
		email  string
	}{{[]string{api.ScopeAppsRead}, "ops@faas.dev"}, {api.ScopesAdminOnly, "other@faas.dev"}} {
		e := apidserver.NewObsEnv(t, tc.scopes, "ops@faas.dev", tc.email)
		rec := e.Do(t, http.MethodGet, "/v1/admin/obs/incidents/deployment/"+uuid.NewString()+"/evidence", nil, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func TestObsIncidentEvidenceRequiresMFA(t *testing.T) {
	h, account, manager, sessionID := apidserver.SetupMW(t, api.PlanPro, true)
	pending := apidserver.ReissueWithMFAFlag(t, manager, sessionID, account.ID, true)
	rec := apidserver.CookieDo(t, h, pending, http.MethodGet, "/v1/admin/obs/incidents/deployment/"+uuid.NewString()+"/evidence", nil)
	var problem api.Problem
	if rec.Code != http.StatusForbidden || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeMFARequired {
		t.Fatalf("pending MFA: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

type obsEvidenceUnavailable struct{ Store state.Store }

func (s *obsEvidenceUnavailable) DeploymentByID(context.Context, string) (state.Deployment, error) {
	return state.Deployment{}, errors.New("private database failure")
}

func TestObsIncidentEvidenceStoreFailureIsNotMissingResource(t *testing.T) {
	e := gapEnv(t)
	*(*e.S()).Store() = statetest.NewStore(&obsEvidenceUnavailable{Store: *(*e.S()).Store()})
	rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/incidents/deployment/"+uuid.NewString()+"/evidence", nil, nil)
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "private database failure") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
