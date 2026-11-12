package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// An agent key with deploy:write could deploy a whole project, promote an
// environment, switch release sets or repoint GitOps without a person's
// approval, because the production list named only app routes (deploy
// review 2026-11-10, D3). Each now asks.
func TestHandlers_AgentProjectWritesNeedApproval(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "project-approver@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "project-agent", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/projects/shop/environments/production/promote"},
		{http.MethodPost, "/v1/projects/shop/environments/production/release-sets/activate"},
		{http.MethodPost, "/v1/projects/shop/environments/production/promotions/0b5b8c1e-6c39-4c51-9d1a-1f6f3c1c2a10/abort"},
		{http.MethodPatch, "/v1/projects/shop/environments/production/gitops/source"},
		{http.MethodPost, "/v1/projects/safe-deploy"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			r.Header.Set("Authorization", "Bearer "+plaintext)
			r.Header.Set("Content-Type", "application/json")
			srv.ServeHTTP(rec, r)
			var problem api.Problem
			if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil ||
				problem.Code != api.CodeApprovalRequired || problem.AgentApproval == nil ||
				problem.AgentApproval.Category != api.AgentApprovalProduction {
				t.Fatalf("response = %d %s; want 428 approval_required (production)", rec.Code, rec.Body)
			}
		})
	}
}
