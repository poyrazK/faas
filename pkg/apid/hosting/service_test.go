package hosting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type testHost struct {
	enabled, allowed bool
	store            *state.MemStore
	artifacts        *testArtifacts
}

func (h *testHost) HostingEnabled() bool { return h.enabled }
func (h *testHost) AdminAllows(state.Account) (bool, *api.Problem) {
	return h.allowed, api.NewProblem(403, "admin_required", "Internal access required", "")
}
func (h *testHost) HostingStore() state.HostingStore                             { return h.store }
func (h *testHost) HostingArtifacts() artifacts.ArtifactStore                    { return h.artifacts }
func (h *testHost) HostingDomain() string                                        { return "hosting.example" }
func (h *testHost) HostingOriginID() string                                      { return strings.Repeat("a", 64) }
func (h *testHost) HostingAudit(context.Context, string, string, map[string]any) {}
func (h *testHost) HostingProject(ctx context.Context, account, slug, environment string) (state.Project, error) {
	p, err := h.store.ProjectBySlug(ctx, account, slug)
	if err != nil {
		return p, err
	}
	_, err = h.store.ProjectEnvironmentBySlug(ctx, account, p.ID, environment)
	return p, err
}

type testArtifacts struct {
	calls        int
	publishErr   error
	afterPublish func()
}

func (a *testArtifacts) Upload(context.Context, string, string, api.HostingFile) (api.HostingUpload, error) {
	a.calls++
	return api.HostingUpload{}, nil
}
func (a *testArtifacts) Publish(context.Context, string, string, api.HostingManifest) error {
	a.calls++
	if a.afterPublish != nil {
		a.afterPublish()
	}
	return a.publishErr
}

func TestEveryHostingHandlerRequiresFlagAndOperator(t *testing.T) {
	h := &testHost{store: state.NewMemStore(), artifacts: &testArtifacts{}}
	s := Service{Host: h}
	handlers := []httpkit.AccountHandler{s.CreateSite, s.ListSites, s.GetSite, s.CreateRelease, s.ListReleases, s.Uploads, s.Finalize, s.Promote, s.Retire, s.DeleteSite}
	for index, handler := range handlers {
		for _, check := range []struct {
			enabled, allowed bool
			status           int
		}{{false, true, 503}, {true, false, 403}} {
			h.enabled, h.allowed = check.enabled, check.allowed
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest("POST", "/", strings.NewReader("{}")), state.Account{ID: uuid.NewString()})
			if w.Code != check.status {
				t.Fatalf("handler %d: got %d, want %d", index, w.Code, check.status)
			}
		}
	}
	if h.artifacts.calls != 0 {
		t.Fatal("disabled or external requests reached storage")
	}
}

func TestFinalizeCannotPublishReadyAfterFailureOrDisable(t *testing.T) {
	h := &testHost{enabled: true, allowed: true, store: state.NewMemStore(), artifacts: &testArtifacts{}}
	acct, err := h.store.CreateAccount(t.Context(), "operator@hosting.example", api.PlanHobby)
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
	digest := sha256.Sum256([]byte("ok"))
	release, err := h.store.CreateHostingRelease(t.Context(), acct.ID, site.ID, api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 2, SHA256: hex.EncodeToString(digest[:]), ContentType: "text/html"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := Service{Host: h}
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		r.SetPathValue("site", site.ID)
		r.SetPathValue("release", release.ID)
		return r
	}
	h.artifacts.publishErr = errors.New("provider unavailable")
	w := httptest.NewRecorder()
	s.Finalize(w, request(), acct)
	if w.Code != 503 {
		t.Fatalf("failure status %d", w.Code)
	}
	h.artifacts.publishErr = nil
	h.artifacts.afterPublish = func() { h.enabled = false }
	w = httptest.NewRecorder()
	s.Finalize(w, request(), acct)
	if w.Code != 503 {
		t.Fatalf("disabled status %d", w.Code)
	}
	stored, err := h.store.GetHostingRelease(t.Context(), acct.ID, site.ID, release.ID)
	if err != nil || stored.State != "pending" {
		t.Fatalf("acknowledged failed publication: %+v %v", stored, err)
	}
	h.artifacts.afterPublish = nil
	h.enabled = true
	w = httptest.NewRecorder()
	s.Finalize(w, request(), acct)
	if w.Code != 200 {
		t.Fatalf("retry status %d: %s", w.Code, w.Body)
	}
	other := state.Account{ID: uuid.NewString()}
	w = httptest.NewRecorder()
	s.Finalize(w, request(), other)
	if w.Code != 404 {
		t.Fatalf("cross-account status %d", w.Code)
	}
}
