// adr: 20261017201005
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestRehearsalNetworkEnrollmentRequiresOperatorGate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	const run = "abababababababababababababababab"
	r := e.Do(t, "PUT", "/v1/dev/sessions/network-api", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", r.Code, r.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	req := api.RegisterScenarioTestRequest{WebhookMode: "capture", NetworkMode: "contained", Members: []api.ScenarioTestWorkload{{Workload: "primary", AppSlug: session.App.Slug}}}
	r = e.Do(t, "PUT", "/v1/dev/test-runs/"+run, req, nil)
	if r.Code != http.StatusNotImplemented || !strings.Contains(r.Body.String(), api.CodeRehearsalNotEnabled) {
		t.Fatalf("unqualified fleet accepted containment: %d %s", r.Code, r.Body.String())
	}
	s := *e.S()
	*s.RehearsalNetworkEnabled() = true
	r = e.Do(t, "PUT", "/v1/dev/test-runs/"+run, req, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("enrollment: %d %s", r.Code, r.Body.String())
	}
	var ack api.RegisterScenarioTestResponse
	if err := json.Unmarshal(r.Body.Bytes(), &ack); err != nil || ack.NetworkMode != "contained" {
		t.Fatalf("missing containment acknowledgement: %+v %v", ack, err)
	}
	store := *e.Store()
	app, err := store.AppBySlug(t.Context(), session.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.ScenarioRehearsalNetworkRun(t.Context(), app.ID); err != nil || got != run {
		t.Fatalf("acknowledged before durable enrollment: %q %v", got, err)
	}
}
