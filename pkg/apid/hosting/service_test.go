package hosting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	cancellation              bool
	reservations              bool
	activity                  bool
	activityReader            state.HostingActivityReader
	ciSetup                   bool
	oidcStore                 state.HostingOIDCStore
	diagnostics               bool
	diagnosticReader          DiagnosticReader
	oidc                      bool
	automation                bool
	retention                 bool
	enabled, allowed, durable bool
	analytics                 bool
	history                   bool
	previews                  bool
	config                    bool
	trafficReader             artifacts.TrafficReader
	domains                   bool
	domainProvider            artifacts.DomainProvider
	store                     *state.MemStore
	artifacts                 *testArtifacts
}

func (h *testHost) HostingEnabled() bool               { return h.enabled }
func (h *testHost) HostingDurableRoutingEnabled() bool { return h.durable }
func (h *testHost) AdminAllows(state.Account) (bool, *api.Problem) {
	return h.allowed, api.NewProblem(403, "admin_required", "Internal access required", "")
}
func (h *testHost) HostingStore() state.HostingStore                { return h.store }
func (h *testHost) HostingArtifacts() artifacts.ArtifactStore       { return h.artifacts }
func (h *testHost) HostingDomain() string                           { return "hosting.example" }
func (h *testHost) HostingOriginID() string                         { return strings.Repeat("a", 64) }
func (h *testHost) HostingDomainsEnabled() bool                     { return h.domains }
func (h *testHost) HostingDomainProvider() artifacts.DomainProvider { return h.domainProvider }
func (h *testHost) HostingDomainStore() state.HostingDomainStore    { return h.store }
func (h *testHost) HostingReservedDomains() []string {
	return []string{"gregale.dev", "hosting.example"}
}
func (h *testHost) HostingUploadBaseURL() string {
	return "https://example.r2.cloudflarestorage.com/bucket"
}
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
	handlers := []httpkit.AccountHandler{s.GetDiagnostics, s.ListOIDCTrusts, s.CreateOIDCTrust, s.RevokeOIDCTrust, s.ListAutomationCredentials, s.CreateAutomationCredential, s.RevokeAutomationCredential, s.CreateCIRun, s.GetCIRun, s.GetCleanupPlan, s.SetRetentionPolicy, s.CreateCIPreview, s.ListHistory, s.ListPreviews, s.CreatePreview, s.RevokePreview, s.ListDomains, s.CreateDomain, s.DeleteDomain, s.Capabilities, s.Usage, s.GetTraffic, s.RecoverDomain, s.CreateSite, s.ListSites, s.GetSite, s.GetStatus, s.CreateRelease, s.ListReleases, s.Uploads, s.Finalize, s.Promote, s.Retire, s.DeleteSite, s.SetSuspended}
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

func TestDurableCreationFlagAndExplicitSuspension(t *testing.T) {
	for _, durable := range []bool{false, true} {
		t.Run(map[bool]string{false: "default_leased", true: "durable"}[durable], func(t *testing.T) {
			h := &testHost{enabled: true, allowed: true, durable: durable, store: state.NewMemStore(), artifacts: &testArtifacts{}}
			account, err := h.store.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanHobby)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "web"}); err != nil {
				t.Fatal(err)
			}
			s := Service{Host: h}
			w := httptest.NewRecorder()
			s.CreateSite(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"project":"web","environment":"production","name":"web"}`)), account)
			if w.Code != 201 {
				t.Fatalf("create: %d %s", w.Code, w.Body)
			}
			var site api.HostingSite
			if err = json.Unmarshal(w.Body.Bytes(), &site); err != nil {
				t.Fatal(err)
			}
			if site.DurableRouting != durable {
				t.Fatal("new durable sites bypassed the additional flag")
			}
			h.durable = false
			request := func(body string) *http.Request {
				r := httptest.NewRequest("PUT", "/", strings.NewReader(body))
				r.SetPathValue("site", site.ID)
				return r
			}
			w = httptest.NewRecorder()
			s.SetSuspended(w, request(`{"expected_revision":1}`), account)
			if w.Code != 400 {
				t.Fatalf("missing suspension value: %d", w.Code)
			}
			w = httptest.NewRecorder()
			s.SetSuspended(w, request(`{"suspended":true,"expected_revision":1}`), account)
			want := 202
			if !durable {
				want = 409
			}
			if w.Code != want {
				t.Fatalf("suspend: %d %s", w.Code, w.Body)
			}
			if durable {
				loaded, _ := h.store.GetHostingSite(t.Context(), account.ID, site.ID)
				if !loaded.Suspended || loaded.Revision != 2 {
					t.Fatal("suspension intent did not commit")
				}
			}
		})
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

func (h *testHost) HostingAnalyticsEnabled() bool                 { return h.analytics }
func (h *testHost) HostingTrafficReader() artifacts.TrafficReader { return h.trafficReader }

func (h *testHost) HostingConfigEnabled() bool { return h.config }

func (h *testHost) HostingPreviewsEnabled() bool                   { return h.previews }
func (h *testHost) HostingPreviewStore() state.HostingPreviewStore { return h.store }

func (h *testHost) HostingPreviewReservationsEnabled() bool                          { return h.reservations }
func (h *testHost) HostingPreviewCapacityReader() state.HostingPreviewCapacityReader { return h.store }

func (h *testHost) HostingCancellationEnabled() bool                         { return h.cancellation }
func (h *testHost) HostingCancellationStore() state.HostingCancellationStore { return h.store }
