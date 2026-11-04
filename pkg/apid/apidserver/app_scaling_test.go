// adr: 20261104104521
package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRecoverableScalingHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "scale-http")
	path := "/v1/apps/scale-http/scaling"
	read := e.Do(t, http.MethodGet, path, nil, nil)
	var snapshot api.AppScalingSnapshot
	if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &snapshot) != nil {
		t.Fatalf("read: %d %s", read.Code, read.Body)
	}
	ram := 512
	req := api.ApplyAppScalingRequest{ExpectedAppID: appID, RequestID: uuid.NewString(), ExpectedRevision: &snapshot.Revision, RAMMB: &ram}
	first := e.Do(t, http.MethodPost, path, req, nil)
	var receipt api.AppScalingReceipt
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &receipt) != nil || receipt.AppID != appID || receipt.Settings.RAMMB != ram {
		t.Fatalf("apply: %d %s", first.Code, first.Body)
	}
	// A normal PATCH must invalidate the scaling revision, while recovery
	// must continue returning the original receipt without applying again.
	other := 768
	if out := e.Do(t, http.MethodPatch, "/v1/apps/scale-http", api.UpdateAppRequest{RAMMB: &other}, nil); out.Code != 200 {
		t.Fatalf("legacy PATCH: %d %s", out.Code, out.Body)
	}
	if out := e.Do(t, http.MethodPost, path, req, nil); out.Code != 200 || out.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %s", out.Code, out.Body)
	}
	if out := e.Do(t, http.MethodGet, path+"/requests/"+req.RequestID, nil, nil); out.Code != 200 || out.Body.String() != first.Body.String() {
		t.Fatalf("receipt: %d %s", out.Code, out.Body)
	}
	req.RequestID = uuid.NewString()
	if out := e.Do(t, http.MethodPost, path, req, nil); out.Code != 409 || !strings.Contains(out.Body.String(), "scaling_revision_conflict") {
		t.Fatalf("stale: %d %s", out.Code, out.Body)
	}
	req.RequestID = receipt.RequestID
	req.RAMMB = &other
	if out := e.Do(t, http.MethodPost, path, req, nil); out.Code != 409 || !strings.Contains(out.Body.String(), "scaling_request_conflict") {
		t.Fatalf("changed request: %d %s", out.Code, out.Body)
	}
	if out := e.Do(t, http.MethodGet, path+"/requests/not-a-uuid", nil, nil); out.Code != 400 {
		t.Fatalf("invalid ID: %d %s", out.Code, out.Body)
	}
	store := *e.Store()
	current, err := store.AppByID(t.Context(), appID)
	if err != nil || current.RAMMB != other {
		t.Fatalf("recovery changed app: %+v %v", current, err)
	}
}

func TestScalingHTTPUsesNativePlanAdmission(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	appID := apidserver.MustSeedApp(t, e, "scale-free")
	store := *e.Store()
	before, err := store.AppScaling(t.Context(), e.Acct().ID, appID, "default")
	if err != nil {
		t.Fatal(err)
	}
	minimum := 1
	req := api.ApplyAppScalingRequest{ExpectedAppID: appID, RequestID: uuid.NewString(), ExpectedRevision: &before.Revision, MinInstances: &minimum}
	out := e.Do(t, http.MethodPost, "/v1/apps/scale-free/scaling", req, nil)
	if out.Code != 403 || !strings.Contains(out.Body.String(), api.CodePlanMinInstancesNotAllowed) {
		t.Fatalf("free floor admitted: %d %s", out.Code, out.Body)
	}
	if _, err := store.AppScalingReceipt(t.Context(), e.Acct().ID, appID, "default", req.RequestID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refusal receipt: %v", err)
	}
}

func TestScalingHTTPAppBoundAgentAuthorization(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "scale-agent")
	apidserver.MustSeedApp(t, e, "scale-other")
	created := e.Do(t, http.MethodPost, "/v1/apps/scale-agent/deploy-tokens", api.CreateDeployTokenRequest{Agent: true, Scopes: []string{api.ScopeDeployWrite}}, nil)
	var token api.DeployTokenResponse
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &token) != nil {
		t.Fatalf("mint: %d %s", created.Code, created.Body)
	}
	snapshot := (*e.Store())
	before, err := snapshot.AppScaling(t.Context(), e.Acct().ID, appID, "default")
	if err != nil {
		t.Fatal(err)
	}
	ram := 512
	req := api.ApplyAppScalingRequest{ExpectedAppID: appID, RequestID: uuid.NewString(), ExpectedRevision: &before.Revision, RAMMB: &ram}
	if out := e.DoAs(t, http.MethodPost, "/v1/apps/scale-agent/scaling", req, nil, token.Plaintext); out.Code != 200 {
		t.Fatalf("app-bound scaling write: %d %s", out.Code, out.Body)
	}
	if out := e.DoAs(t, http.MethodGet, "/v1/apps/scale-agent/scaling/requests/"+req.RequestID, nil, nil, token.Plaintext); out.Code != 200 {
		t.Fatalf("app-bound receipt: %d %s", out.Code, out.Body)
	}
	if out := e.DoAs(t, http.MethodPost, "/v1/apps/scale-other/scaling", req, nil, token.Plaintext); out.Code != 404 {
		t.Fatalf("cross-app scaling write: %d %s", out.Code, out.Body)
	}

	for _, tc := range []struct {
		app    string
		status int
	}{{"scale-agent", 200}, {"scale-other", 404}} {
		out := e.DoAs(t, http.MethodGet, "/v1/apps/"+tc.app+"/scaling", nil, nil, token.Plaintext)
		if out.Code != tc.status {
			t.Fatalf("app-bound %s: %d %s", tc.app, out.Code, out.Body)
		}
	}
}
