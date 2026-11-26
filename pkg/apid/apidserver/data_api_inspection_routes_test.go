package apidserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataAPIInspectionRoutesRequireScopesAndStore(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	seedAppTaskDeployment(t, e, "inspect-routes")
	path := "/v1/apps/inspect-routes/data-api-inspections"
	body := api.CreateDataAPIInspectionRequest{DatabaseID: "00000000-0000-0000-0000-000000000001", BindingID: "00000000-0000-0000-0000-000000000002", Table: "notes"}
	// In-memory stores do not implement the atomic admission fence, so even
	// an administrator must not receive a verification task.
	if rec := e.do(t, http.MethodPost, path, body, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing admission store: %d %s", rec.Code, rec.Body.String())
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "inspect-deploy-only", []string{api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"database_id":"00000000-0000-0000-0000-000000000001","binding_id":"00000000-0000-0000-0000-000000000002","table":"notes"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing managed postgres scope: %d %s", rec.Code, rec.Body.String())
	}
	unauth := httptest.NewRecorder()
	e.h.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, path+"/00000000-0000-0000-0000-000000000003", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated read: %d %s", unauth.Code, unauth.Body.String())
	}
}
