package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeployTokenHandlers_CreateListRotateRevoke(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "tokenized-app")

	createdRec := e.Do(t, http.MethodPost, "/v1/apps/tokenized-app/deploy-tokens", api.CreateDeployTokenRequest{Label: "github-ci"}, nil)
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", createdRec.Code, createdRec.Body.String())
	}
	var created api.DeployTokenResponse
	if err := json.Unmarshal(createdRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Plaintext == "" || !api.ValidDeployTokenFormat(created.Plaintext) {
		t.Fatalf("created response missing valid plaintext: %+v", created)
	}
	if created.Prefix != api.DeployTokenPrefix || created.Label != "github-ci" || created.Status != "active" {
		t.Fatalf("created response = %+v", created)
	}

	listRec := e.Do(t, http.MethodGet, "/v1/apps/tokenized-app/deploy-tokens", nil, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: code=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Tokens []api.DeployTokenResponse `json:"tokens"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Tokens) != 1 || listed.Tokens[0].Plaintext != "" {
		t.Fatalf("list = %+v, want one redacted token", listed)
	}

	rotatedRec := e.Do(t, http.MethodPost, "/v1/apps/tokenized-app/deploy-tokens/"+created.ID+"/rotate", api.RotateDeployTokenRequest{ExpiresAt: time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)}, nil)
	if rotatedRec.Code != http.StatusCreated {
		t.Fatalf("rotate: code=%d body=%s", rotatedRec.Code, rotatedRec.Body.String())
	}
	var rotated api.RotateDeployTokenResponse
	if err := json.Unmarshal(rotatedRec.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.TokenPlaintext == "" || rotated.OldTokenID != created.ID || rotated.Token.Status != "active" {
		t.Fatalf("rotate response = %+v", rotated)
	}

	revokedRec := e.Do(t, http.MethodDelete, "/v1/apps/tokenized-app/deploy-tokens/"+rotated.Token.ID, nil, nil)
	if revokedRec.Code != http.StatusNoContent {
		t.Fatalf("revoke: code=%d body=%s", revokedRec.Code, revokedRec.Body.String())
	}
	if again := e.Do(t, http.MethodDelete, "/v1/apps/tokenized-app/deploy-tokens/"+rotated.Token.ID, nil, nil); again.Code != http.StatusNoContent {
		t.Fatalf("idempotent revoke: code=%d body=%s", again.Code, again.Body.String())
	}
}

func TestDeployTokenHandlers_CrossAppIsNotFound(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "token-app-a")
	apidserver.MustSeedApp(t, e, "token-app-b")
	createdRec := e.Do(t, http.MethodPost, "/v1/apps/token-app-a/deploy-tokens", api.CreateDeployTokenRequest{}, nil)
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", createdRec.Code, createdRec.Body.String())
	}
	var created api.DeployTokenResponse
	_ = json.Unmarshal(createdRec.Body.Bytes(), &created)
	if rec := e.Do(t, http.MethodGet, "/v1/apps/token-app-b/deploy-tokens", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("same-account app B list: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := (*e.Store()).GetDeployToken(t.Context(), (*e.Acct()).ID, "wrong-app", created.ID); err == nil {
		t.Fatal("cross-app store lookup unexpectedly succeeded")
	}
}

// A deploy token bound to app A must not reach app B in the same account
// through any /v1/apps route — including handlers that resolve the app with a
// direct store lookup instead of loadApp (createDeployment did, so a CI token
// for one app could ship code to every app in the account).
func TestDeployTokenBearer_BoundToItsApp(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "bound-app-a")
	appB := apidserver.MustSeedApp(t, e, "bound-app-b")
	createdRec := e.Do(t, http.MethodPost, "/v1/apps/bound-app-a/deploy-tokens", api.CreateDeployTokenRequest{Label: "ci-a"}, nil)
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", createdRec.Code, createdRec.Body.String())
	}
	var created api.DeployTokenResponse
	if err := json.Unmarshal(createdRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	bearer := func(method, path string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&buf).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, path, &buf)
		req.Header.Set("Authorization", "Bearer "+created.Plaintext)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{"own app deployments list", http.MethodGet, "/v1/apps/bound-app-a/deployments", nil, http.StatusOK},
		{"other app deployments list", http.MethodGet, "/v1/apps/bound-app-b/deployments", nil, http.StatusNotFound},
		{"other app deploy", http.MethodPost, "/v1/apps/bound-app-b/deployments", api.CreateDeploymentRequest{Image: imageRef('c')}, http.StatusNotFound},
		{"account-wide app metrics", http.MethodGet, "/v1/apps/metrics", nil, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := bearer(tc.method, tc.path, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("%s %s = %d, want %d (body=%s)", tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	if deps, err := (*e.Store()).ListDeploymentsForApp(t.Context(), appB, 10, 0); err != nil || len(deps) != 0 {
		t.Fatalf("app B has %d deployments (err=%v) after requests with app A's token", len(deps), err)
	}
}

// Every CLI deploy reads its app, then polls the deployment it created
// (ADR-20261031162858). A deploy token could do neither, so `gregale deploy`
// with FAAS_TOKEN set to one failed insufficient_scope, and the official
// deploy Action's OIDC bearer (deploy:write) failed the same app read. The
// token reaches only its own app and that app's deployments.
func TestDeployCredentials_ReadTheirAppAndDeployments(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appA := apidserver.MustSeedApp(t, e, "ci-app-a")
	appB := apidserver.MustSeedApp(t, e, "ci-app-b")
	depA, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: appA, Status: state.DeployLive, RootfsPath: "/artifact"})
	if err != nil {
		t.Fatal(err)
	}
	depB, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: appB, Status: state.DeployLive, RootfsPath: "/artifact"})
	if err != nil {
		t.Fatal(err)
	}
	createdRec := e.Do(t, http.MethodPost, "/v1/apps/ci-app-a/deploy-tokens", api.CreateDeployTokenRequest{Label: "ci"}, nil)
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", createdRec.Code, createdRec.Body.String())
	}
	var token api.DeployTokenResponse
	if err := json.Unmarshal(createdRec.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	deployKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "ci-deploy-only", []string{api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, bearer, path string
		want               int
	}{
		{"token reads its app", token.Plaintext, "/v1/apps/ci-app-a", http.StatusOK},
		{"token cannot read another app", token.Plaintext, "/v1/apps/ci-app-b", http.StatusNotFound},
		{"token reads its app's deployment", token.Plaintext, "/v1/deployments/" + depA.ID, http.StatusOK},
		{"token cannot read another app's deployment", token.Plaintext, "/v1/deployments/" + depB.ID, http.StatusNotFound},
		{"token with a malformed deployment id", token.Plaintext, "/v1/deployments/not-a-deployment", http.StatusNotFound},
		{"token cannot read build logs", token.Plaintext, "/v1/deployments/" + depA.ID + "/logs", http.StatusForbidden},
		{"token cannot list deployments account-wide", token.Plaintext, "/v1/deployments", http.StatusForbidden},
		{"deploy:write key reads the app it deploys", deployKey, "/v1/apps/ci-app-a", http.StatusOK},
		{"deploy:write key reads a deployment", deployKey, "/v1/deployments/" + depA.ID, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.Do(t, http.MethodGet, tc.path, nil, map[string]string{"Authorization": "Bearer " + tc.bearer})
			if rec.Code != tc.want {
				t.Fatalf("GET %s = %d, want %d (body=%s)", tc.path, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	// The read reveals nothing PATCH does not: deploy:write already updates
	// the app and receives the same document.
	if rec := e.Do(t, http.MethodPatch, "/v1/apps/ci-app-a", api.UpdateAppRequest{}, map[string]string{"Authorization": "Bearer " + deployKey}); rec.Code != http.StatusOK {
		t.Fatalf("deploy:write PATCH = %d (body=%s)", rec.Code, rec.Body.String())
	}
}
