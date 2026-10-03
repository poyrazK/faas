// ADR-465/ADR-466: dashboard creation preserves immutable region intent and
// enforces the shared admission policy, including a pause racing insertion.
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise the atomic activity mutation as well as the admission preflight.
type wizardRegionPauseStore struct{ *state.MemStore }

func (s wizardRegionPauseStore) CreateAppIfUnderQuotaWithActivity(ctx context.Context, app state.App, limits api.Limits, entry state.OrgActivity) (state.App, int64, error) {
	region, err := s.ComputeRegionByID(ctx, app.ComputeRegion)
	if err != nil {
		return state.App{}, 0, err
	}
	region.AcceptingNewApps = false
	if _, err := s.UpsertComputeRegion(ctx, region); err != nil {
		return state.App{}, 0, err
	}
	return s.MemStore.CreateAppIfUnderQuotaWithActivity(ctx, app, limits, entry)
}

type wizardRegionReadFailureStore struct{ state.Store }

func (s wizardRegionReadFailureStore) ComputeRegionByID(context.Context, string) (state.ComputeRegion, error) {
	return state.ComputeRegion{}, errors.New("registry unavailable")
}

func (s wizardRegionReadFailureStore) ListComputeRegions(context.Context) ([]state.ComputeRegion, error) {
	return nil, errors.New("registry unavailable")
}

type wizardRegionNodeReadFailureStore struct{ state.Store }

func (s wizardRegionNodeReadFailureStore) ListAllComputeNodes(context.Context) ([]state.ComputeNode, error) {
	return nil, errors.New("node observations unavailable")
}

func regionalWizardServer(t *testing.T) (*server, *state.MemStore, *session.Manager, state.Account, string, *appsNewFake) {
	t.Helper()
	gh := &appsNewFake{repos: []Repo{{FullName: "octocat/hello", DefaultBranch: "main"}}}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice", defaultBranch: "main", bindReturn: "regional-binding"}
	srv, store, mgr, acct, cookie := newGitHubWizardServerFor(t, gh, true)
	srv.regionalComputeEnabled = true
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{
		AccountID: acct.ID, InstallationID: 42, AuditGithubLogin: "alice", SealedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return srv, store, mgr, acct, cookie, gh
}

func postRegionalWizard(t *testing.T, srv *server, mgr *session.Manager, acct state.Account, cookie, region string) *httptest.ResponseRecorder {
	t.Helper()
	csrf, err := middleware.IssueForAuthenticatedNamed(mgr, githubWizardCreateAction, acct.ID, githubWizardCreateCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"csrf_token": {csrf}, "installation_id": {"42"}, "repo_full_name": {"octocat/hello"},
		"production_branch": {"main"}, "slug": {"regional-app"}, "compute_region": {region},
	}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/apps/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	req.AddCookie(&http.Cookie{Name: githubWizardCreateCSRFCookie, Value: csrf})
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	return rec
}

func TestCreateAppFromGitHubWizard_ComputeRegion(t *testing.T) {
	for _, tc := range []struct {
		name, region, errorContains string
		configure                   func(*testing.T, *server, *state.MemStore)
	}{
		{name: "selected", region: "local"},
		{name: "automatic"},
		{name: "automatic with gate disabled", configure: func(_ *testing.T, s *server, _ *state.MemStore) { s.regionalComputeEnabled = false }},
		{name: "disabled", region: "local", errorContains: "not been enabled", configure: func(_ *testing.T, s *server, _ *state.MemStore) { s.regionalComputeEnabled = false }},
		{name: "noncanonical", region: "Local", errorContains: "compute_region"},
		{name: "whitespace", region: " local ", errorContains: "compute_region"},
		{name: "unknown", region: "missing", errorContains: "not registered"},
		{name: "unavailable", region: "empty", errorContains: "no eligible", configure: func(t *testing.T, _ *server, m *state.MemStore) {
			if _, err := m.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "empty", DisplayName: "Empty", AcceptingNewApps: true}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "paused", region: "local", errorContains: "not accepting", configure: func(t *testing.T, _ *server, m *state.MemStore) {
			if _, err := m.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "local", DisplayName: "Local", AcceptingNewApps: false}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "pause before insert", region: "local", errorContains: "not accepting", configure: func(_ *testing.T, s *server, m *state.MemStore) { s.store = wizardRegionPauseStore{m} }},
		{name: "registry failure", region: "local", errorContains: "could not validate", configure: func(_ *testing.T, s *server, m *state.MemStore) { s.store = wizardRegionReadFailureStore{m} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, store, mgr, acct, cookie, gh := regionalWizardServer(t)
			if tc.configure != nil {
				tc.configure(t, srv, store)
			}
			rec := postRegionalWizard(t, srv, mgr, acct, cookie, tc.region)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			location, err := url.Parse(rec.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			app, lookupErr := store.AppBySlug(t.Context(), "regional-app")
			if tc.errorContains == "" {
				if lookupErr != nil {
					t.Fatal(lookupErr)
				}
				if location.Path != "/dashboard/apps/regional-app" || location.Query().Get("github") != "connected" || app.ComputeRegion != tc.region || gh.bindCalls != 1 {
					t.Fatalf("location=%s region=%q bindings=%d", location, app.ComputeRegion, gh.bindCalls)
				}
				return
			}
			if !errors.Is(lookupErr, state.ErrNotFound) || gh.bindCalls != 0 {
				t.Fatalf("rejected creation mutated state: app=%+v err=%v bindings=%d", app, lookupErr, gh.bindCalls)
			}
			query := location.Query()
			if location.Path != "/dashboard/apps/new" || !strings.Contains(query.Get("error"), tc.errorContains) || query.Get("compute_region") != tc.region || query.Get("slug") != "regional-app" || query.Get("repo") != "octocat/hello" {
				t.Fatalf("error redirect lost intent or admission reason: %s", location)
			}
		})
	}
}

// Inspect browser form semantics: a selected disabled option is not submitted.
func assertWizardRegionControls(t *testing.T, body, region string, wantSelector bool) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var selectors, selected, reloads, createTokens int
	var form *html.Node
	checkForm := func(n *html.Node) {
		for n != nil && !(n.Type == html.ElementNode && n.Data == "form") {
			n = n.Parent
		}
		if n == nil {
			t.Fatal("wizard control has no enclosing form")
		}
		if form != nil && form != n {
			t.Fatal("installation reload and creation would submit different controls")
		}
		form = n
	}
	var visit func(*html.Node, bool)
	visit = func(n *html.Node, inRegion bool) {
		attrs := map[string]string{}
		for _, attr := range n.Attr {
			attrs[attr.Key] = attr.Val
		}
		if n.Type == html.ElementNode && n.Data == "select" && attrs["name"] == "compute_region" {
			checkForm(n)
			selectors++
			inRegion = true
		}
		if n.Type == html.ElementNode && n.Data == "button" && attrs["formmethod"] == "GET" {
			checkForm(n)
			reloads++
		}
		if n.Type == html.ElementNode && n.Data == "button" && attrs["name"] == "csrf_token" && attrs["value"] != "" {
			checkForm(n)
			createTokens++
		}
		if n.Type == html.ElementNode && n.Data == "input" && attrs["name"] == "csrf_token" {
			t.Fatal("GET reload would expose CSRF token in its URL")
		}
		if inRegion && n.Type == html.ElementNode && n.Data == "option" {
			if _, ok := attrs["selected"]; ok {
				selected++
				if _, disabled := attrs["disabled"]; disabled || attrs["value"] != region {
					t.Errorf("selected option loses choice: %v", attrs)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c, inRegion)
		}
	}
	visit(doc, false)
	wantCount := 0
	if wantSelector {
		wantCount = 1
	}
	if selectors != wantCount || selected != wantCount || reloads != 1 || createTokens != 1 {
		t.Fatalf("selectors=%d selected=%d reloads=%d createTokens=%d; want=%d, %d, 1, 1", selectors, selected, reloads, createTokens, wantCount, wantCount)
	}
}

func TestRenderAppNew_ComputeRegionSelection(t *testing.T) {
	for _, tc := range []struct {
		name, region, wantText string
		configure              func(*testing.T, *server, *state.MemStore)
		wantSelector           bool
	}{
		{name: "available", region: "local", wantText: "Available for new apps", wantSelector: true},
		{name: "automatic", wantSelector: true},
		{name: "unknown", region: "missing", wantText: "selection unavailable", wantSelector: true},
		{name: "escaped unknown", region: `"><script>alert(1)</script>`, wantText: "selection unavailable", wantSelector: true},
		{name: "unavailable", region: "empty", wantText: "Unavailable", wantSelector: true, configure: func(t *testing.T, _ *server, m *state.MemStore) {
			if _, err := m.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "empty", DisplayName: "Empty", AcceptingNewApps: true}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "paused", region: "local", wantText: "New apps paused", wantSelector: true, configure: func(t *testing.T, _ *server, m *state.MemStore) {
			if _, err := m.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "local", DisplayName: "Local", AcceptingNewApps: false}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "gate disabled after selection", region: "local", wantText: "currently disabled", wantSelector: true, configure: func(_ *testing.T, s *server, _ *state.MemStore) { s.regionalComputeEnabled = false }},
		{name: "gate disabled legacy", configure: func(_ *testing.T, s *server, _ *state.MemStore) { s.regionalComputeEnabled = false }},
		{name: "catalog failure", region: "local", wantText: "could not be loaded", wantSelector: true, configure: func(_ *testing.T, s *server, m *state.MemStore) { s.store = wizardRegionReadFailureStore{m} }},
		{name: "node observation failure", region: "local", wantText: "could not be loaded", wantSelector: true, configure: func(_ *testing.T, s *server, m *state.MemStore) { s.store = wizardRegionNodeReadFailureStore{m} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, store, _, _, cookie, _ := regionalWizardServer(t)
			if tc.configure != nil {
				tc.configure(t, srv, store)
			}
			q := url.Values{"compute_region": {tc.region}, "install": {"42"}}
			req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/new?"+q.Encode(), nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
			rec := httptest.NewRecorder()
			srv.handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			assertWizardRegionControls(t, body, tc.region, tc.wantSelector)
			if !strings.Contains(body, tc.wantText) || strings.Contains(body, "<script>alert(1)</script>") {
				t.Fatalf("wrong availability copy or unsafe rendering: %s", body)
			}
		})
	}
}

func TestRenderAppNew_InstallationReloadKeepsAppIntent(t *testing.T) {
	srv, _, _, _, cookie, _ := regionalWizardServer(t)
	q := url.Values{
		"installation_id": {"42"}, "repo_full_name": {"octocat/hello"},
		"production_branch": {"release"}, "slug": {"regional-app"}, "compute_region": {"local"},
	}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/new?"+q.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	assertWizardRegionControls(t, body, "local", true)
	for _, want := range []string{`value="42" selected`, `value="octocat/hello" selected`, `name="production_branch" value="release"`, `name="slug" value="regional-app"`} {
		if !strings.Contains(body, want) {
			t.Errorf("installation reload lost %q", want)
		}
	}
}

func TestGitHubWizard_ComputeRegionReturnAndErrors(t *testing.T) {
	returnTo := "/dashboard/apps/new?compute_region=eu-test&install=42"
	if got := githubConnectedRedirect(returnTo, url.Values{"github": {"connected"}}); got != "/dashboard/apps/new?compute_region=eu-test&github=connected&install=42" {
		t.Fatalf("OAuth return lost selection: %s", got)
	}
	// Earlier errors, including an expired CSRF form, must preserve region too.
	req := httptest.NewRequest(http.MethodPost, "/dashboard/apps/new", strings.NewReader("compute_region=eu-test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	redirectGitHubWizardError(rec, req, "expired", "", "", "", "")
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := location.Query().Get("compute_region"); got != "eu-test" {
		t.Fatalf("form error lost selection: %q", got)
	}
}

func TestAppListItem_StoredComputeRegionWithGateDisabled(t *testing.T) {
	srv, _, _, _, _, _ := regionalWizardServer(t)
	srv.regionalComputeEnabled = false
	item := srv.appListItem(t.Context(), state.App{ID: "app", Slug: "regional-app", ComputeRegion: "eu-test"}, nil, time.Time{})
	if item.ComputeRegion != "eu-test" {
		t.Fatalf("stored selection hidden: %+v", item)
	}
}
