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

func (h *testHost) HostingDiagnosticsEnabled() bool { return h.diagnostics }
func (h *testHost) HostingDiagnosticReader() DiagnosticReader {
	if h.diagnosticReader != nil {
		return h.diagnosticReader
	}
	return h.store
}

type failingDiagnosticReader struct {
	DiagnosticReader
	fail  bool
	after func()
}

func (r failingDiagnosticReader) ListHostingPreviews(ctx context.Context, account, site string) ([]api.HostingPreview, error) {
	if r.fail {
		return nil, errors.New("private database credentials")
	}
	rows, err := r.DiagnosticReader.ListHostingPreviews(ctx, account, site)
	if r.after != nil {
		r.after()
	}
	return rows, err
}

func TestHostingDiagnosticsAreReadOnlyScopedAndFailClosed(t *testing.T) {
	// adr: 202610171059 — diagnostics observes existing intent and never retries allocation/publication.
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	h := &testHost{enabled: true, allowed: true, durable: true, automation: true, previews: true, store: store, artifacts: &testArtifacts{}}
	s := Service{Host: h}
	run, err := store.CreateHostingCIRun(t.Context(), account.ID, site.ID, uuid.NewString(), api.HostingCIRunRequest{Manifest: manifestForDiagnostics(), PreviewTTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	get := func(query string, a state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/diagnostics"+query, nil)
		r.SetPathValue("site", site.ID)
		w := httptest.NewRecorder()
		s.GetDiagnostics(w, r, a)
		return w
	}
	if w := get("", account); w.Code != 503 {
		t.Fatal("default off", w.Code)
	}
	h.diagnostics = true
	if w := get("", state.Account{ID: uuid.NewString()}); w.Code != 404 {
		t.Fatal("foreign read", w.Code)
	}
	for _, query := range []string{"?run_id=bad", "?run_id=", "?run_id=" + run.ID + "&run_id=" + run.ID, "?unknown=x", "?run_id=%xx"} {
		if w := get(query, account); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	before, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := get("?run_id="+run.ID, account)
	var report api.HostingDiagnostics
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &report) != nil || report.ReleaseID != run.ReleaseID || report.PreviewID != run.PreviewID || report.RetryAdvice != "retry_same_run" || report.ServingHealth != "not_checked" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	after, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil || after.Generation != before.Generation || h.artifacts.calls != 0 {
		t.Fatal("read caused publication work", err)
	}
	retained, err := store.GetHostingCIRun(t.Context(), account.ID, site.ID, run.ID)
	if err != nil || retained != run {
		t.Fatal("run changed", err)
	}
	releases, err := store.ListHostingReleases(t.Context(), account.ID, site.ID)
	if err != nil || len(releases) != 1 || releases[0].State != "pending" {
		t.Fatal("release mutated", err)
	}
	previews, err := store.ListHostingPreviews(t.Context(), account.ID, site.ID)
	if err != nil || len(previews) != 0 {
		t.Fatal("preview created", err)
	}
	h.diagnosticReader = failingDiagnosticReader{DiagnosticReader: store, fail: true}
	if w = get("?run_id="+run.ID, account); w.Code != 503 || strings.Contains(w.Body.String(), "private database credentials") {
		t.Fatal("storage error became missing preview", w.Code, w.Body)
	}
	h.previews = false
	w = get("?run_id="+run.ID, account)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "previews_unavailable") || strings.Contains(w.Body.String(), "preview_removed") {
		t.Fatal("disabled preview read was treated as removal", w.Code, w.Body)
	}
	h.previews = true
	h.automation = false
	w = get("?run_id="+run.ID, account)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "automation_unavailable") || strings.Contains(w.Body.String(), "run_not_found") {
		t.Fatal("disabled automation read was treated as absence", w.Code, w.Body)
	}
	h.automation = true
	h.diagnosticReader = failingDiagnosticReader{DiagnosticReader: store, after: func() {
		_, e := store.SetHostingSiteSuspended(t.Context(), account.ID, site.ID, site.Revision, true)
		if e != nil {
			t.Fatal(e)
		}
	}}
	w = get("?run_id="+run.ID, account)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &report) != nil || report.RetryAdvice != "inspect_before_retry" {
		t.Fatal("concurrent state change ignored", w.Code, w.Body)
	}
	h.diagnosticReader = failingDiagnosticReader{DiagnosticReader: store, after: func() { h.diagnostics = false }}
	if w = get("?run_id="+run.ID, account); w.Code != 503 {
		t.Fatal("in-flight kill switch", w.Code)
	}
}

func manifestForDiagnostics() api.HostingManifest {
	return api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 3, ContentType: "text/html"}}}
}
