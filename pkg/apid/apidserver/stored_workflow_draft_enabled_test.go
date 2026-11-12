package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ChronoLake finding 29(3): an unpublished draft whose trigger has
// enabled: false must not be reported as enabled; first publication starts it
// disabled.
func TestStoredWorkflowDraftReportsDefinitionEnabledState(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "draft-enabled")
	if _, err := (*e.Store()).CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	spec := api.WorkflowSpec{Name: "nightly", Trigger: &api.WorkflowTriggerSpec{Type: "schedule", Schedule: "0 9 * * *", Enabled: &disabled}, Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/report"}}}
	base := "/v1/apps/" + app.Slug + "/workflows/stored"
	response := e.Do(t, "PUT", base+"/nightly", api.SaveStoredWorkflowDraftRequest{Definition: spec}, nil)
	var draft api.StoredWorkflowResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &draft) != nil {
		t.Fatalf("save=%d %s", response.Code, response.Body.String())
	}
	if draft.Enabled {
		t.Fatalf("draft with trigger.enabled=false reported enabled: %s", response.Body.String())
	}
	response = e.Do(t, "GET", base+"/nightly", nil, nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &draft) != nil || draft.Enabled {
		t.Fatalf("get=%d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, "POST", base+"/nightly/publish", api.PublishStoredWorkflowRequest{ExpectedVersion: draft.Version}, nil)
	var published api.StoredWorkflowResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &published) != nil || published.Enabled {
		t.Fatalf("publish=%d %s", response.Code, response.Body.String())
	}
}
