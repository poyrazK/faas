package agentconnections_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agentconnections"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppAgentsAppearInConnectionsAndAreRevoked(t *testing.T) {
	f := newFixture(t)
	app, err := f.store.CreateApp(t.Context(), state.App{AccountID: f.acct.ID, Slug: "app-agent", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	scopes, err := api.NormalizeAppAgentScopes(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := api.GenerateDeployToken()
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.store.CreateDeployToken(t.Context(), f.acct.ID, app.ID, hash, "coding-agent", scopes, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	out := f.call(agentconnections.List(f.deps), http.MethodGet, "/v1/agent-connections", nil, "")
	var list api.AgentConnectionsResponse
	if err := json.Unmarshal(out.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != token.ID || list.Items[0].AppID != app.ID || list.Items[0].Kind != api.AgentConnectionAppAgent {
		t.Fatalf("app-agent inventory=%+v", list)
	}
	for range 2 {
		out = f.call(agentconnections.Revoke(f.deps), http.MethodDelete, "/v1/agent-connections/"+token.ID, nil, "")
		if out.Code != 204 {
			t.Fatalf("revoke %d %s", out.Code, out.Body.String())
		}
	}
	if _, _, err := f.store.AuthenticateDeployToken(t.Context(), hash); !errors.Is(err, state.ErrAPIKeyRevoked) {
		t.Fatalf("credential survived disconnect: %v", err)
	}
}
