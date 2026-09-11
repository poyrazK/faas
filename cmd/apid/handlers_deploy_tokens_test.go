package main

import (
	"encoding/json"
	"net/http"
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
