package hosting

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingCancellationGatesAndTerminalActivity(t *testing.T) {
	// adr: 202610171242 — cancellation is operator-only, default-off and independent of preview admission.
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	h := &testHost{enabled: true, allowed: true, durable: true, automation: true, activity: true, store: store, artifacts: &testArtifacts{}}
	h.activityReader = store
	s := Service{Host: h}
	run, err := store.CreateHostingCIRun(t.Context(), account.ID, site.ID, uuid.NewString(), api.HostingCIRunRequest{Manifest: manifestForDiagnostics(), PreviewTTLSeconds: 3600, ReservePreview: true})
	if err != nil {
		t.Fatal(err)
	}
	request := func(a state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/", nil)
		r.SetPathValue("site", site.ID)
		r.SetPathValue("run", run.ID)
		w := httptest.NewRecorder()
		s.CancelCIRun(w, r, a)
		return w
	}
	if w := request(account); w.Code != 503 {
		t.Fatal("default off", w.Code, w.Body)
	}
	h.cancellation = true
	h.allowed = false
	if w := request(account); w.Code != 403 {
		t.Fatal("nonoperator", w.Code, w.Body)
	}
	h.allowed = true
	if w := request(state.Account{ID: uuid.NewString()}); w.Code != 404 {
		t.Fatal("foreign", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("site", site.ID)
	w := httptest.NewRecorder()
	s.ListActivity(w, r, account)
	var page api.HostingActivityPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Entries) != 1 || !page.Entries[0].CanCancel {
		t.Fatal("eligibility", w.Code, w.Body)
	}
	w = request(account)
	var canceled api.HostingCIRun
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &canceled) != nil || !canceled.Canceled || canceled.ID != run.ID || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	s.ListActivity(w, r, account)
	page = api.HostingActivityPage{}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Entries[0].State != "canceled" || !page.Entries[0].Canceled || page.Entries[0].CanCancel {
		t.Fatal("terminal activity", w.Code, w.Body)
	}
	h.cancellation = false
	if w = request(account); w.Code != 503 {
		t.Fatal("hot off replay", w.Code, w.Body)
	}
	if _, err = store.CreateHostingCIPreview(t.Context(), account.ID, site.ID, run.ID); err == nil {
		t.Fatal("hot off lost terminal fence")
	}
	if h.artifacts.calls != 0 {
		t.Fatal("provider called")
	}
}
