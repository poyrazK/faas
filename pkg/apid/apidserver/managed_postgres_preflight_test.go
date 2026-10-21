package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func preflightAPIGet(env apidserver.SourceRefTestEnv, id string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/v1/postgres/cutovers/"+id+"/preflight", nil)
	r.Header.Set("Authorization", "Bearer "+(*env.Key()))
	w := httptest.NewRecorder()
	(*env.H()).ServeHTTP(w, r)
	return w
}

func decodePreflight(t *testing.T, w *httptest.ResponseRecorder, status int) api.ManagedPostgresCutoverPreflight {
	t.Helper()
	if w.Code != status || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	var p api.ManagedPostgresCutoverPreflight
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.ActivationAllowed {
		t.Fatal("activation enabled")
	}
	return p
}

func TestManagedPostgresPreflightAPIRecoveryReplayAndScope(t *testing.T) {
	env, service, req := cutoverAPIFixture(t)
	c := decodeCutoverAPI(t, env.Post(t, "/v1/postgres/cutovers", req), http.StatusAccepted)
	path := "/v1/postgres/cutovers/" + c.ID + "/preflight"
	if w := preflightAPIGet(env, c.ID); w.Code != http.StatusNotFound {
		t.Fatal("missing preflight", w.Code)
	}
	p := decodePreflight(t, env.PostWithIdempotency(t, path, nil, "preflight-first"), http.StatusAccepted)
	if p.State != "pending" || p.Revision != 1 || !p.Stale {
		t.Fatal(p)
	}
	if err := service.Sweep(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	p = decodePreflight(t, preflightAPIGet(env, c.ID), http.StatusOK)
	if p.State != "blocked" || p.Stale || len(p.Blockers) == 0 || len(p.BindingIDs) != 2 {
		t.Fatal(p)
	}
	// Idempotency replay is the original observation; polling is authoritative
	// for current preflight progress, and neither response authorizes activation.
	p = decodePreflight(t, env.PostWithIdempotency(t, path, nil, "preflight-first"), http.StatusAccepted)
	if p.State != "pending" || p.Revision != 1 {
		t.Fatal("replay reran work", p)
	}
	p = decodePreflight(t, env.PostWithIdempotency(t, path, nil, "preflight-next"), http.StatusAccepted)
	if p.Revision != 2 {
		t.Fatal("explicit retry did not refresh", p)
	}
	if _, err := service.Cancel(t.Context(), (*env.AcctID()), c.ID); err != nil {
		t.Fatal(err)
	}
	if w := env.Post(t, path, nil); w.Code != http.StatusConflict {
		t.Fatal("cancelled refresh", w.Code)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*env.Store()).CreateAPIKey(t.Context(), (*env.AcctID()), hash, "preflight-read", []string{api.ScopeManagedPostgresRead}); err != nil {
		t.Fatal(err)
	}
	(*env.Key()) = key
	decodePreflight(t, preflightAPIGet(env, c.ID), http.StatusOK)
	if w := env.Post(t, path, nil); w.Code != http.StatusForbidden {
		t.Fatal("read-only key mutated", w.Code)
	}
	other, err := (*env.Store()).CreateAccount(t.Context(), "other-preflight@test.invalid", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err = api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*env.Store()).CreateAPIKey(t.Context(), other.ID, hash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	(*env.Key()) = key
	if w := preflightAPIGet(env, c.ID); w.Code != http.StatusNotFound {
		t.Fatal("tenant read leak", w.Code)
	}
	if w := env.Post(t, path, nil); w.Code != http.StatusNotFound {
		t.Fatal("tenant mutation leak", w.Code)
	}
	if w := preflightAPIGet(env, "invalid"); w.Code != http.StatusBadRequest {
		t.Fatal("invalid ID", w.Code)
	}
}
