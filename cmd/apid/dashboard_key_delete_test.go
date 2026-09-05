package main

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedDashboardAPIKey(t *testing.T, store *state.MemStore, acct state.Account, label string) state.APIKey {
	t.Helper()
	hash := sha256.Sum256([]byte(label))
	key, err := store.CreateAPIKey(t.Context(), acct.ID, hash[:], label, []string{api.ScopeAdmin})
	if err != nil {
		t.Fatalf("CreateAPIKey(%q): %v", label, err)
	}
	return key
}

func renderDashboardKeyDelete(t *testing.T, h http.Handler, sid *http.Cookie, keyID string) (*http.Cookie, string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/account", nil)
	req.AddCookie(sid)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/account: status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	var csrfCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == dashboardKeyDeleteCSRFCookie {
			csrfCookie = cookie
			break
		}
	}
	if csrfCookie == nil {
		t.Fatalf("GET /dashboard/account: missing %s cookie", dashboardKeyDeleteCSRFCookie)
	}
	action := "/dashboard/account/keys/" + keyID + "/delete"
	token := extractInputValue(rec.Body.String(), middleware.FormFieldName, action)
	return csrfCookie, token, rec.Body.String()
}

func TestDashboardDeleteKey_RendersNamedCSRFAndTypedConfirmation(t *testing.T) {
	h, sid, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	key := seedDashboardAPIKey(t, store, acct, "ci-deploy")
	_, token, body := renderDashboardKeyDelete(t, h, sid, key.ID)
	if token == "" {
		t.Fatal("key-delete form is missing csrf_token")
	}
	prefix := api.APIKeyPrefix + hexPrefix(key.Hash)
	for _, want := range []string{`name="confirmation"`, "required", prefix} {
		if !strings.Contains(body, want) {
			t.Errorf("account page missing %q", want)
		}
	}
}

func TestDashboardDeleteKey_RevokesOnlySelectedKey(t *testing.T) {
	h, sid, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	selected := seedDashboardAPIKey(t, store, acct, "selected")
	sibling := seedDashboardAPIKey(t, store, acct, "sibling")
	csrfCookie, token, _ := renderDashboardKeyDelete(t, h, sid, selected.ID)
	rec := dashboardPOST(t, h, sid, "/dashboard/account/keys/"+selected.ID+"/delete", map[string]string{
		middleware.FormFieldName: token,
		dashboardKeyConfirmField: api.APIKeyPrefix + hexPrefix(selected.Hash),
	}, csrfCookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/dashboard/account?key_revoked=1" {
		t.Errorf("Location = %q", got)
	}
	selectedAfter, err := store.GetAPIKey(t.Context(), acct.ID, selected.ID)
	if err != nil {
		t.Fatal(err)
	}
	if selectedAfter.Status != string(state.APIKeyStatusRevoked) {
		t.Errorf("selected status = %q, want revoked", selectedAfter.Status)
	}
	siblingAfter, err := store.GetAPIKey(t.Context(), acct.ID, sibling.ID)
	if err != nil {
		t.Fatal(err)
	}
	if siblingAfter.Status == string(state.APIKeyStatusRevoked) {
		t.Fatal("sibling key was revoked")
	}
	events, err := store.ListEvents(t.Context(), acct.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Kind != "key.revoked" {
		t.Fatalf("events = %+v, want key.revoked", events)
	}
	var data map[string]any
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["key_id"] != selected.ID {
		t.Errorf("audit key_id = %v, want %s", data["key_id"], selected.ID)
	}
}

func TestDashboardDeleteKey_RejectsWrongConfirmation(t *testing.T) {
	h, sid, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	key := seedDashboardAPIKey(t, store, acct, "keep-me")
	csrfCookie, token, _ := renderDashboardKeyDelete(t, h, sid, key.ID)
	rec := dashboardPOST(t, h, sid, "/dashboard/account/keys/"+key.ID+"/delete", map[string]string{
		middleware.FormFieldName: token,
		dashboardKeyConfirmField: "wrong-prefix",
	}, csrfCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	after, err := store.GetAPIKey(t.Context(), acct.ID, key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status == string(state.APIKeyStatusRevoked) {
		t.Fatal("key was revoked despite a mismatched confirmation")
	}
}

func TestDashboardDeleteKey_RequiresNamedCSRFCookie(t *testing.T) {
	h, sid, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	key := seedDashboardAPIKey(t, store, acct, "keep-me")
	_, token, _ := renderDashboardKeyDelete(t, h, sid, key.ID)
	rec := dashboardPOST(t, h, sid, "/dashboard/account/keys/"+key.ID+"/delete", map[string]string{
		middleware.FormFieldName: token,
		dashboardKeyConfirmField: api.APIKeyPrefix + hexPrefix(key.Hash),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
}

func TestDashboardDeleteKey_CollapsesForeignKeyToNotFound(t *testing.T) {
	h, sid, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	owned := seedDashboardAPIKey(t, store, acct, "owned")
	other, err := store.CreateAccount(t.Context(), "other@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	foreign := seedDashboardAPIKey(t, store, other, "foreign")
	csrfCookie, token, _ := renderDashboardKeyDelete(t, h, sid, owned.ID)
	rec := dashboardPOST(t, h, sid, "/dashboard/account/keys/"+foreign.ID+"/delete", map[string]string{
		middleware.FormFieldName: token,
		dashboardKeyConfirmField: api.APIKeyPrefix + hexPrefix(foreign.Hash),
	}, csrfCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404\nbody = %s", rec.Code, rec.Body.String())
	}
}
