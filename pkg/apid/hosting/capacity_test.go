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

func TestPreviewReservationAdmissionHotDisableAndCapacity(t *testing.T) {
	// adr: 202610171216 — new admission is gated; exact retries and holds survive hot disable.
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	h := &testHost{enabled: true, allowed: true, durable: true, automation: true, previews: true, store: store, artifacts: &testArtifacts{}}
	s := Service{Host: h}
	id := uuid.NewString()
	intent := api.HostingCIRunRequest{Manifest: manifestForDiagnostics(), PreviewTTLSeconds: 3600, ReservePreview: true}
	call := func(run string) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(intent)
		r := httptest.NewRequest("PUT", "/", strings.NewReader(string(raw)))
		r.SetPathValue("site", site.ID)
		r.SetPathValue("run", run)
		w := httptest.NewRecorder()
		s.CreateCIRun(w, r, account)
		return w
	}
	if w := call(id); w.Code != 503 {
		t.Fatal("default off", w.Code, w.Body)
	}
	if rows, err := store.ListHostingReleases(t.Context(), account.ID, site.ID); err != nil || len(rows) != 0 {
		t.Fatal("disabled allocated", rows, err)
	}
	h.reservations = true
	w := call(id)
	var run api.HostingCIRun
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &run) != nil || !run.PreviewReserved {
		t.Fatal(w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("site", site.ID)
	w = httptest.NewRecorder()
	s.PreviewCapacity(w, r, account)
	var capacity api.HostingPreviewCapacity
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &capacity) != nil || capacity.Reservations != 1 || capacity.Available != api.HostingPreviewsPerSite-1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	h.reservations = false
	if w = call(uuid.NewString()); w.Code != 503 {
		t.Fatal("hot off admitted", w.Code, w.Body)
	}
	if w = call(id); w.Code != 200 {
		t.Fatal("hot off broke retry", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	s.PreviewCapacity(w, r, account)
	if w.Code != 503 {
		t.Fatal("hot off capacity", w.Code)
	}
	if _, err := store.ReadyHostingRelease(t.Context(), account.ID, site.ID, run.ReleaseID); err != nil {
		t.Fatal(err)
	}
	r.SetPathValue("run", id)
	w = httptest.NewRecorder()
	s.CreateCIPreview(w, r, account)
	if w.Code != 202 {
		t.Fatal("hot off broke conversion", w.Code, w.Body)
	}
	if h.artifacts.calls != 0 {
		t.Fatal("provider called")
	}
	h.reservations = true
	h.allowed = false
	w = httptest.NewRecorder()
	s.PreviewCapacity(w, r, account)
	if w.Code != 403 {
		t.Fatal("nonoperator capacity", w.Code)
	}
	h.allowed = true
	w = httptest.NewRecorder()
	s.PreviewCapacity(w, r, state.Account{ID: uuid.NewString()})
	if w.Code != 404 {
		t.Fatal("foreign capacity", w.Code)
	}
}
