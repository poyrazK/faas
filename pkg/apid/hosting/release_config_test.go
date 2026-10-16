package hosting

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReleaseConfigAdmissionAndDisableDuringFinalize(t *testing.T) {
	// adr: 202610161909 — release policies are bounded and independently gated.
	h := &testHost{enabled: true, allowed: true, store: state.NewMemStore(), artifacts: &testArtifacts{}}
	acct, err := h.store.CreateAccount(t.Context(), "config@hosting.example", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.store.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "web"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	site, err := h.store.CreateHostingSite(t.Context(), api.HostingSite{ID: id, AccountID: acct.ID, ProjectID: p.ID, Environment: "production", Name: "web", Hostname: id + ".hosting.example", OriginID: h.HostingOriginID()})
	if err != nil {
		t.Fatal(err)
	}
	s := Service{Host: h}
	manifest := api.HostingManifest{Config: &api.HostingReleaseConfig{Version: 1}, Files: []api.HostingFile{{Path: "index.html", SHA256: strings.Repeat("a", 64), Size: 2, ContentType: "text/html"}}}
	raw, _ := json.Marshal(manifest)
	request := func(body string, release string) *http.Request {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.SetPathValue("site", site.ID)
		r.SetPathValue("release", release)
		return r
	}
	w := httptest.NewRecorder()
	s.CreateRelease(w, request(string(raw), ""), acct)
	if w.Code != 503 {
		t.Fatalf("default-off admission: %d %s", w.Code, w.Body)
	}
	releases, _ := h.store.ListHostingReleases(t.Context(), acct.ID, site.ID)
	if len(releases) != 0 {
		t.Fatal("disabled admission wrote a release")
	}
	h.config = true
	w = httptest.NewRecorder()
	s.CreateRelease(w, request(string(raw), ""), acct)
	if w.Code != 201 {
		t.Fatalf("enabled admission: %d %s", w.Code, w.Body)
	}
	var release api.HostingRelease
	if err = json.Unmarshal(w.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	h.artifacts.afterPublish = func() { h.config = false }
	w = httptest.NewRecorder()
	s.Finalize(w, request("", release.ID), acct)
	if w.Code != 503 {
		t.Fatalf("disable during finalize: %d", w.Code)
	}
	stored, _ := h.store.GetHostingRelease(t.Context(), acct.ID, site.ID, release.ID)
	if stored.State != "pending" {
		t.Fatal("disabled feature acknowledged publication")
	}
	calls := h.artifacts.calls
	w = httptest.NewRecorder()
	s.Finalize(w, request("", release.ID), acct)
	if w.Code != 503 || h.artifacts.calls != calls {
		t.Fatal("disabled finalize reached provider")
	}
	h.config = true
	h.artifacts.afterPublish = nil
	w = httptest.NewRecorder()
	s.Finalize(w, request("", release.ID), acct)
	if w.Code != 200 {
		t.Fatalf("finalize %d %s", w.Code, w.Body)
	}
	h.config = false
	promotion := `{"release_id":"` + release.ID + `","expected_revision":1}`
	w = httptest.NewRecorder()
	s.Promote(w, request(promotion, ""), acct)
	if w.Code != 503 {
		t.Fatalf("disabled promotion %d", w.Code)
	}
	h.config = true
	w = httptest.NewRecorder()
	s.Promote(w, request(promotion, ""), acct)
	if w.Code != 202 {
		t.Fatalf("promotion %d %s", w.Code, w.Body)
	}
	h.config = false
	// Revocation and cleanup stay available when configuration admission is off.
	w = httptest.NewRecorder()
	s.DeleteSite(w, request("", ""), acct)
	if w.Code != 202 {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
}
