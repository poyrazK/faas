package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A deploy token deploys its app. deploy:write opens more than that to API
// keys, but the token may not delete or rename the app, send its data
// elsewhere, mint credentials or read its debug and crash data; another
// app's routes stay 404 so the token cannot probe them.
func TestDeployToken_RefusedOutsideDeploying(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appA := apidserver.MustSeedApp(t, e, "denied-app-a")
	apidserver.MustSeedApp(t, e, "denied-app-b")
	createdRec := e.Do(t, http.MethodPost, "/v1/apps/denied-app-a/deploy-tokens", api.CreateDeployTokenRequest{Label: "ci"}, nil)
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

	refused := []struct{ method, path string }{
		{http.MethodDelete, "/v1/apps/denied-app-a"},
		{http.MethodPost, "/v1/apps/denied-app-a/rename"},
		{http.MethodPost, "/v1/apps/denied-app-a/log-drains"},
		{http.MethodPost, "/v1/apps/denied-app-a/mirrors"},
		{http.MethodPost, "/v1/apps/denied-app-a/webhooks"},
		{http.MethodPost, "/v1/apps/denied-app-a/consumers"},
		{http.MethodPost, "/v1/apps/denied-app-a/ai-access-tokens"},
		{http.MethodPost, "/v1/apps/denied-app-a/issue-ingest-tokens"},
		{http.MethodPost, "/v1/apps/denied-app-a/crash-snapshots"},
		{http.MethodPost, "/v1/apps/denied-app-a/forks"},
	}
	for _, tc := range refused {
		t.Run("refused "+tc.method+" "+tc.path, func(t *testing.T) {
			rec := bearer(tc.method, tc.path, map[string]any{})
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "this token deploys its app") {
				t.Fatalf("%s %s = %d, want the deploy-token 403 (body=%s)", tc.method, tc.path, rec.Code, rec.Body.String())
			}
		})
	}
	if _, err := (*e.Store()).AppByID(t.Context(), appA); err != nil {
		t.Fatalf("app A after the token's delete attempt: %v", err)
	}

	allowed := []struct {
		name         string
		method, path string
		body         any
	}{
		{"deploy", http.MethodPost, "/v1/apps/denied-app-a/deployments", api.CreateDeploymentRequest{Image: imageRef('d')}},
		{"read its app", http.MethodGet, "/v1/apps/denied-app-a", nil},
		{"update app config", http.MethodPatch, "/v1/apps/denied-app-a", map[string]any{}},
	}
	for _, tc := range allowed {
		t.Run("allowed "+tc.name, func(t *testing.T) {
			rec := bearer(tc.method, tc.path, tc.body)
			if rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound || rec.Code == http.StatusUnauthorized {
				t.Fatalf("%s %s = %d, want it admitted (body=%s)", tc.method, tc.path, rec.Code, rec.Body.String())
			}
		})
	}

	if rec := bearer(http.MethodDelete, "/v1/apps/denied-app-b", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete another app = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}
