package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestConnectionBudgetAPIIsReadOnlyAccountScopedAndCredentialFree(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "budget-api", 9001)
	store, _, database := apidserver.ConfigureSourceRefManagedPostgres(t, env)
	now := time.Now().UTC()
	b := managedpostgres.Binding{ID: uuid.NewString(), AccountID: *env.AcctID(), DatabaseID: database, AppID: *env.AppID(), Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialDataAPI, CredentialGeneration: 1, State: managedpostgres.BindingStateProvisioning, CreatedAt: now, UpdatedAt: now, RetryAt: now}
	if _, _, err := store.ReserveBinding(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	paths := []string{"/v1/postgres/databases/" + database + "/connection-budget", "/v1/apps/budget-api/data-api/connection-budget"}
	request := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		(*env.H()).ServeHTTP(w, r)
		return w
	}
	for _, path := range paths {
		w := request(path, *env.Key())
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
		var report map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report["state"] != "unknown" {
			t.Fatal(report)
		}
		for _, private := range []string{"credential_ref", "provider_identity_id", "postgres://", "backend_fingerprint"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("private metadata escaped")
			}
		}
		if request(path, "").Code != http.StatusUnauthorized {
			t.Fatal("authentication missing")
		}
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*env.Store()).CreateAPIKey(t.Context(), *env.AcctID(), hash, "budget-read", []string{api.ScopeManagedPostgresRead}); err != nil {
		t.Fatal(err)
	}
	if w := request(paths[0], key); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	other, err := (*env.Store()).CreateAccount(t.Context(), "other-budget@test.invalid", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, otherHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*env.Store()).CreateAPIKey(t.Context(), other.ID, otherHash, "other-budget", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if w := request(path, otherKey); w.Code != http.StatusNotFound {
			t.Fatal("cross-account read", w.Code, w.Body.String())
		}
	}
	unchanged, err := store.GetBinding(t.Context(), *env.AcctID(), b.ID)
	if err != nil || unchanged.State != b.State || unchanged.CredentialGeneration != b.CredentialGeneration || unchanged.CredentialRef != "" {
		t.Fatal("read-only report changed the binding", unchanged, err)
	}
}

func TestRuntimeConnectionReportRouteRejectsCustomerCredentials(t *testing.T) {
	env := apidserver.Setup(t, api.PlanPro)
	for _, authorization := range []string{"", "Bearer fp_customer-key"} {
		r := httptest.NewRequest(http.MethodPost, "/v1/runtime/data-api/connection-observations", strings.NewReader(`{}`))
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		w := httptest.NewRecorder()
		(*env.H()).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
