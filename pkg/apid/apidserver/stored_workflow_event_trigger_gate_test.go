package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// A stored workflow whose event trigger matches customer events is refused
// while the account has custom events off: schedd would hold and then
// dead-letter every delivery, so it would never start. Platform sources
// (gregale.*) stay allowed.
func TestStoredWorkflowEventTriggerNeedsEvents(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	srv := *e.S()
	srv.WithWorkflowRuntimeEnabled(true)
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "events"), platformflags.Definition{Enabled: true, Default: false})
	app := seedWorkflowApp(t, e, "event-gate")
	if _, err := (*e.Store()).CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + app.Slug + "/workflows/stored/"
	save := func(name, source string) int64 {
		t.Helper()
		spec := api.WorkflowSpec{Name: name, Trigger: &api.WorkflowTriggerSpec{Type: "event", Source: source, EventType: "*"}, Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/"}}}
		response := e.Do(t, "PUT", base+name, api.SaveStoredWorkflowDraftRequest{Definition: spec}, nil)
		var draft api.StoredWorkflowResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &draft) != nil {
			t.Fatalf("save %s: %d %s", name, response.Code, response.Body.String())
		}
		return draft.Version
	}
	publish := func(name string, version int64) (int, string) {
		response := e.Do(t, "POST", base+name+"/publish", api.PublishStoredWorkflowRequest{ExpectedVersion: version}, nil)
		return response.Code, response.Body.String()
	}

	custom := save("custom", "billing")
	if code, body := publish("custom", custom); code != http.StatusConflict || !strings.Contains(body, api.CodeStoredWorkflowEventsUnavailable) {
		t.Fatalf("custom source with events off: %d %s", code, body)
	}
	wildcard := save("wildcard", "*")
	if code, body := publish("wildcard", wildcard); code != http.StatusConflict {
		t.Fatalf("wildcard source with events off: %d %s", code, body)
	}
	platform := save("platform", "gregale.storage")
	if code, body := publish("platform", platform); code != http.StatusOK {
		t.Fatalf("platform source with events off: %d %s", code, body)
	}

	srv.SeedPlatformFlag(capabilityFlag(t, "events"), platformflags.Definition{Enabled: true, Default: true})
	if code, body := publish("custom", custom); code != http.StatusOK {
		t.Fatalf("custom source with events on: %d %s", code, body)
	}
}
