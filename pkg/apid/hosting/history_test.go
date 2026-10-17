package hosting

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHistoryMetadataGatesAndAuthenticatedActor(t *testing.T) {
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	h := &testHost{enabled: true, allowed: true, store: store, artifacts: &testArtifacts{}}
	s := Service{Host: h}
	request := func(body string) *http.Request {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.SetPathValue("site", site.ID)
		return r
	}
	body := `{"files":[{"path":"index.html","size":1,"sha256":"` + strings.Repeat("a", 64) + `","content_type":"text/html"}],"provenance":{"commit_sha":"` + strings.Repeat("b", 40) + `","description":"unverified"}}`
	w := httptest.NewRecorder()
	s.CreateRelease(w, request(body), account)
	if w.Code != 503 {
		t.Fatal("off admission", w.Code, w.Body)
	}
	reached := false
	cached := s.RequireReleaseProvenance(func(http.ResponseWriter, *http.Request, state.Account) { reached = true })
	cached(httptest.NewRecorder(), request(body), account)
	if reached {
		t.Fatal("off metadata replay reached cache")
	}
	h.history = true
	w = httptest.NewRecorder()
	s.CreateRelease(w, request(body), account)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var release api.HostingRelease
	if err := json.Unmarshal(w.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadyHostingRelease(t.Context(), account.ID, site.ID, release.ID); err != nil {
		t.Fatal(err)
	}
	// Client-supplied actor identities are rejected by the strict request decoder.
	w = httptest.NewRecorder()
	s.Promote(w, request(`{"release_id":"`+release.ID+`","expected_revision":1,"actor":{"account_id":"fake"}}`), account)
	if w.Code != 400 {
		t.Fatal("caller actor accepted", w.Code)
	}
	actorID, keyID := uuid.NewString(), uuid.NewString()
	req := request(`{"release_id":"` + release.ID + `","expected_revision":1}`)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), state.Account{ID: actorID}, &state.APIKey{ID: keyID}, nil))
	w = httptest.NewRecorder()
	s.Promote(w, req, account)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	page, err := store.ListHostingHistory(t.Context(), account.ID, site.ID, 0)
	if err != nil || len(page.Events) != 1 || page.Events[0].Actor.AccountID != actorID || page.Events[0].Actor.KeyID != keyID {
		t.Fatal(page, err)
	}
	for _, suffix := range []string{"?before_revision=0", "?before_revision=-1", "?before_revision=9007199254740992", "?before_revision=2&before_revision=3"} {
		r := httptest.NewRequest("GET", "/"+suffix, nil)
		r.SetPathValue("site", site.ID)
		w = httptest.NewRecorder()
		s.ListHistory(w, r, account)
		if w.Code != 400 {
			t.Fatal(suffix, w.Code)
		}
	}
	w = httptest.NewRecorder()
	s.ListHistory(w, request(""), state.Account{ID: uuid.NewString()})
	if w.Code != 404 {
		t.Fatal("foreign history", w.Code)
	}
	h.history = false
	w = httptest.NewRecorder()
	s.ListHistory(w, request(""), account)
	if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Header())
	}
	// Disabling capture preserves the legacy promotion path and earlier history.
	w = httptest.NewRecorder()
	s.Promote(w, request(`{"release_id":"`+release.ID+`","expected_revision":2}`), account)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	page, _ = store.ListHostingHistory(t.Context(), account.ID, site.ID, 0)
	if len(page.Events) != 1 {
		t.Fatal("capture ignored kill switch")
	}
}
func (h *testHost) HostingHistoryEnabled() bool                    { return h.history }
func (h *testHost) HostingHistoryStore() state.HostingHistoryStore { return h.store }
