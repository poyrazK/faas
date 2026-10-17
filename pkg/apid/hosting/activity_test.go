package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *testHost) HostingActivityEnabled() bool { return h.activity }
func (h *testHost) HostingActivityReader() state.HostingActivityReader {
	if h.activityReader != nil {
		return h.activityReader
	}
	return h.store
}

type activityReaderHook struct {
	reader state.HostingActivityReader
	after  func()
	fail   bool
}

func (h activityReaderHook) ListHostingActivityRecords(ctx context.Context, account, site, before string) (state.HostingActivityRecords, error) {
	if h.fail {
		return state.HostingActivityRecords{}, errors.New("private database credentials")
	}
	rows, err := h.reader.ListHostingActivityRecords(ctx, account, site, before)
	if h.after != nil {
		h.after()
	}
	return rows, err
}
func TestHostingActivityIsScopedReadOnlyAndFailsClosed(t *testing.T) {
	// adr: 202610171147 — activity reads existing records; disabled/failed reads never imply absence.
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	h := &testHost{enabled: true, allowed: true, durable: true, automation: true, previews: true, history: true, store: store, artifacts: &testArtifacts{}}
	s := Service{Host: h}
	run, err := store.CreateHostingCIRun(t.Context(), account.ID, site.ID, uuid.NewString(), api.HostingCIRunRequest{Manifest: manifestForDiagnostics(), PreviewTTLSeconds: 3600, Provenance: &api.HostingReleaseProvenance{Description: "caller source"}})
	if err != nil {
		t.Fatal(err)
	}
	get := func(query string, a state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/runs"+query, nil)
		r.SetPathValue("site", site.ID)
		w := httptest.NewRecorder()
		s.ListActivity(w, r, a)
		return w
	}
	if w := get("", account); w.Code != 503 {
		t.Fatal("default off", w.Code)
	}
	h.activity = true
	if w := get("", state.Account{ID: uuid.NewString()}); w.Code != 404 {
		t.Fatal("foreign read", w.Code)
	}
	for _, query := range []string{"?before=bad", "?before=", "?before=x&before=y", "?unknown=x", "?before=%zz"} {
		if w := get(query, account); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	before, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := get("", account)
	var page api.HostingActivityPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Entries) != 1 || page.Entries[0].RunID != run.ID || page.Entries[0].State != "pending" || page.Entries[0].Provenance.Description != "caller source" || page.ServingHealth != "not_checked" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	for _, private := range []string{run.Fingerprint, "manifest", "origin_id", "hostname"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private data exposed", private)
		}
	}
	after, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil || after.Generation != before.Generation || h.artifacts.calls != 0 {
		t.Fatal("activity changed resources", err)
	}
	h.history = false
	h.previews = false
	w = get("", account)
	if w.Code != 200 || strings.Contains(w.Body.String(), "caller source") || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.SourceLabelsIncluded || page.Entries[0].State != "unknown" {
		t.Fatal("disabled feature treated as inspected", w.Code, w.Body)
	}
	h.activityReader = activityReaderHook{reader: store, fail: true}
	w = get("", account)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private database") || strings.Contains(w.Body.String(), "entries") {
		t.Fatal(w.Code, w.Body)
	}
	h.activityReader = activityReaderHook{reader: store, after: func() { h.activity = false }}
	w = get("", account)
	if w.Code != 503 || strings.Contains(w.Body.String(), "entries") {
		t.Fatal("hot disable", w.Code, w.Body)
	}
	h.activity = true
	h.activityReader = activityReaderHook{reader: store, after: func() { h.history = true }}
	w = get("", account)
	if w.Code != 409 {
		t.Fatal("flag change while reading", w.Code, w.Body)
	}
	h.activityReader = nil
	h.automation = false
	if w = get("", account); w.Code != 503 {
		t.Fatal("automation off", w.Code, w.Body)
	}
}
