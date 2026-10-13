package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationPublishPolicyAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "publish-policy")
	if _, err := (*e.Store()).CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	policyPath := "/v1/apps/" + app.Slug + "/automations:publish-policy"
	if rec := e.Do(t, "PUT", policyPath, api.SetAutomationPublishPolicyRequest{Mode: "coverage"}, nil); rec.Code != 200 {
		t.Fatalf("set policy %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, "PUT", policyPath, api.SetAutomationPublishPolicyRequest{Mode: "optional"}, nil); rec.Code != http.StatusConflict {
		t.Fatalf("policy version not enforced: %d", rec.Code)
	}
	base := "/v1/apps/" + app.Slug + "/automations/checked"
	def := api.WorkflowSpec{Name: "checked", Steps: []api.WorkflowStepSpec{{Name: "send", Path: "/send"}}}
	rec := e.Do(t, "PUT", base, api.SaveAutomationDraftRequest{Definition: def}, nil)
	var draft api.AutomationResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &draft) != nil {
		t.Fatalf("save %d %s", rec.Code, rec.Body)
	}
	if rec = e.Do(t, "POST", base+"/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version}, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), "automation_publish_check_required") {
		t.Fatalf("bypass %d %s", rec.Code, rec.Body)
	}
	request := api.CheckAutomationPublicationRequest{ExpectedVersion: draft.Version, Scenarios: []api.AutomationPublishCheckScenario{{Name: "success", Simulation: api.SimulateAutomationRequest{Definition: def, MockOutputs: map[string]json.RawMessage{"send": json.RawMessage(`{"secret":"not-retained"}`)}}, Expectations: []api.AutomationCheckExpectation{{Step: "send", State: "mocked"}}}}}
	rec = e.Do(t, "POST", base+"/publish-check", request, nil)
	var checked api.CheckAutomationPublicationResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &checked) != nil || checked.Receipt == "" || strings.Contains(rec.Body.String(), "not-retained") {
		t.Fatalf("check %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, "POST", base+"/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version, CheckReceipt: checked.Receipt}, nil)
	if rec.Code != 200 {
		t.Fatalf("publish %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, "GET", base+"/revisions", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"server_verified":true`) || strings.Contains(rec.Body.String(), "not-retained") {
		t.Fatalf("evidence %d %s", rec.Code, rec.Body)
	}
	if rec = e.Do(t, "GET", policyPath, nil, map[string]string{"Authorization": ""}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated %d", rec.Code)
	}
}

func TestAutomationPublishPolicyScopes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "policy-scopes")
	for _, scope := range []string{api.ScopeDeployWrite, api.ScopeAppsRead} {
		key, hash, _ := api.GenerateAPIKey()
		if _, err := (*e.Store()).CreateAPIKey(context.Background(), (*e.Acct()).ID, hash, "limited-policy", []string{scope}); err != nil {
			t.Fatal(err)
		}
		(*e.Key()) = key
		path := "/v1/apps/" + app.Slug + "/automations:publish-policy"
		if rec := e.Do(t, "PUT", path, api.SetAutomationPublishPolicyRequest{Mode: "optional"}, nil); rec.Code != 403 {
			t.Fatalf("%s changed policy: %d %s", scope, rec.Code, rec.Body)
		}
	}
}
