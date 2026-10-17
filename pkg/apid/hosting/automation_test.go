package hosting

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *testHost) HostingAutomationEnabled() bool                       { return h.automation }
func (h *testHost) HostingAutomationStore() state.HostingAutomationStore { return h.store }
func TestAutomationHandlersFlagsAndOneTimeCredentialResponse(t *testing.T) {
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	h := &testHost{enabled: true, allowed: true, store: store, artifacts: &testArtifacts{}}
	s := Service{Host: h}
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"label":"CI"}`))
	req.SetPathValue("site", site.ID)
	w := httptest.NewRecorder()
	s.CreateAutomationCredential(w, req, account)
	if w.Code != 503 {
		t.Fatal("default off", w.Code)
	}
	h.automation = true
	req = httptest.NewRequest("POST", "/", strings.NewReader(`{"label":"CI","operations":["promote"]}`))
	req.SetPathValue("site", site.ID)
	w = httptest.NewRecorder()
	s.CreateAutomationCredential(w, req, account)
	if w.Code != 400 {
		t.Fatal("production permission issued", w.Code)
	}
	req = httptest.NewRequest("POST", "/", strings.NewReader(`{"label":"CI"}`))
	req.SetPathValue("site", site.ID)
	w = httptest.NewRecorder()
	s.CreateAutomationCredential(w, req, account)
	if w.Code != 201 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	var credential api.HostingAutomationCredentialResponse
	if err := json.Unmarshal(w.Body.Bytes(), &credential); err != nil || !api.ValidHostingAutomationToken(credential.Token) {
		t.Fatal(err)
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("site", site.ID)
	w = httptest.NewRecorder()
	s.ListAutomationCredentials(w, req, account)
	if w.Code != 200 || strings.Contains(w.Body.String(), credential.Token) || strings.Contains(w.Body.String(), "token_hash") {
		t.Fatal("inventory exposed material", w.Code)
	}
	req.SetPathValue("credential", credential.Credential.ID)
	w = httptest.NewRecorder()
	s.RevokeAutomationCredential(w, req, account)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, _, err := store.AuthenticateHostingAutomationToken(t.Context(), api.HashAPIKey(credential.Token)); err == nil {
		t.Fatal("revoked credential accepted")
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("site", site.ID)
	req.SetPathValue("run", uuid.NewString())
	w = httptest.NewRecorder()
	s.GetCIRun(w, req, state.Account{ID: uuid.NewString()})
	if w.Code != 404 {
		t.Fatal("foreign read", w.Code)
	}
}
