package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeployTokenHandlers_CreateListRotateRevoke(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "tokenized-app")

	createdRec := e.do(t, http.MethodPost, "/v1/apps/tokenized-app/deploy-tokens", api.CreateDeployTokenRequest{Label: "github-ci"}, nil)
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

	listRec := e.do(t, http.MethodGet, "/v1/apps/tokenized-app/deploy-tokens", nil, nil)
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

	rotatedRec := e.do(t, http.MethodPost, "/v1/apps/tokenized-app/deploy-tokens/"+created.ID+"/rotate", api.RotateDeployTokenRequest{ExpiresAt: time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)}, nil)
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

	revokedRec := e.do(t, http.MethodDelete, "/v1/apps/tokenized-app/deploy-tokens/"+rotated.Token.ID, nil, nil)
	if revokedRec.Code != http.StatusNoContent {
		t.Fatalf("revoke: code=%d body=%s", revokedRec.Code, revokedRec.Body.String())
	}
	if again := e.do(t, http.MethodDelete, "/v1/apps/tokenized-app/deploy-tokens/"+rotated.Token.ID, nil, nil); again.Code != http.StatusNoContent {
		t.Fatalf("idempotent revoke: code=%d body=%s", again.Code, again.Body.String())
	}
}

func TestDeployTokenHandlers_CrossAppIsNotFound(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "token-app-a")
	mustSeedApp(t, e, "token-app-b")
	createdRec := e.do(t, http.MethodPost, "/v1/apps/token-app-a/deploy-tokens", api.CreateDeployTokenRequest{}, nil)
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", createdRec.Code, createdRec.Body.String())
	}
	var created api.DeployTokenResponse
	_ = json.Unmarshal(createdRec.Body.Bytes(), &created)
	if rec := e.do(t, http.MethodGet, "/v1/apps/token-app-b/deploy-tokens", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("same-account app B list: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := e.store.GetDeployToken(t.Context(), e.acct.ID, "wrong-app", created.ID); err == nil {
		t.Fatal("cross-app store lookup unexpectedly succeeded")
	}
}

// A deploy token bound to app A must not reach app B in the same account
// through any /v1/apps route — including handlers that resolve the app with a
// direct store lookup instead of loadApp (createDeployment did, so a CI token
// for one app could ship code to every app in the account).
func TestDeployTokenBearer_BoundToItsApp(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "bound-app-a")
	appB := mustSeedApp(t, e, "bound-app-b")
	createdRec := e.do(t, http.MethodPost, "/v1/apps/bound-app-a/deploy-tokens", api.CreateDeployTokenRequest{Label: "ci-a"}, nil)
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
		e.h.ServeHTTP(rec, req)
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
	if deps, err := e.store.ListDeploymentsForApp(t.Context(), appB, 10, 0); err != nil || len(deps) != 0 {
		t.Fatalf("app B has %d deployments (err=%v) after requests with app A's token", len(deps), err)
	}
}
