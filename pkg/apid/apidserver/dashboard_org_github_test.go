// adr: 20261110200000
package apidserver_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

var orgGitHubDisconnectToken = regexp.MustCompile(`action="/dashboard/orgs/[^"]+/github/202/disconnect">\s*<input type="hidden" name="csrf_token" value="([^"]+)"`)

// The organization page shows the company's GitHub installations; owners and
// admins can connect for the company and disconnect, which unlinks every
// company project bound through the installation (ADR-20261110200000 slice 2).
func TestOrganizationPageConnectsAndDisconnectsCompanyGitHub(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, &orgInstallFake{}, mgr, nil, 15*time.Minute, "")
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_acme"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_acme"); err != nil {
		t.Fatal(err)
	}
	admin, _ := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	developer, _ := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: org.ID, InstallationID: 202, AuditGithubLogin: "octo", SealedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// One project in the company's namespace, one migrated project that
	// still stores its resources under its creator's namespace.
	bound := func(accountID, slug string) state.Project {
		t.Helper()
		p, err := store.CreateProject(ctx, state.Project{AccountID: accountID, OrgID: org.ID, Slug: slug, ProductionBranch: "main"})
		if err != nil {
			t.Fatal(err)
		}
		if p, err = store.UpdateProjectBinding(ctx, accountID, p.ID, "acme/"+slug, "main", 202); err != nil {
			t.Fatal(err)
		}
		return p
	}
	native, migrated := bound(org.ID, "shop"), bound(admin.ID, "legacy")

	session := func(acct state.Account) *http.Cookie {
		t.Helper()
		value, err := apidserver.MintDashboardSession(ctx, store, mgr, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: apidserver.SessionCookie, Value: value}
	}
	page := func(c *http.Cookie) (*httptest.ResponseRecorder, string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/dashboard/orgs/acme", nil)
		r.AddCookie(c)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("organization page = %d %s", rec.Code, rec.Body)
		}
		return rec, rec.Body.String()
	}
	disconnect := func(c *http.Cookie, prior *httptest.ResponseRecorder, token string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"csrf_token": {token}}
		r := httptest.NewRequest(http.MethodPost, "/dashboard/orgs/acme/github/202/disconnect", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(c)
		if prior != nil {
			for _, set := range prior.Result().Cookies() {
				r.AddCookie(set)
			}
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		return rec
	}

	// A developer sees the installation but no connect or disconnect.
	devCookie := session(developer)
	_, body := page(devCookie)
	if !strings.Contains(body, "<code>202</code>") || strings.Contains(body, "/github/202/disconnect") || strings.Contains(body, `name="organization"`) {
		t.Fatalf("developer view of the GitHub panel is wrong:\n%s", body)
	}
	if rec := disconnect(devCookie, nil, "forged"); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "github=forbidden") {
		t.Fatalf("developer disconnect = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if installs, _ := store.ListGitHubInstallationsForAccount(ctx, org.ID); len(installs) != 1 {
		t.Fatal("a refused disconnect removed the installation")
	}

	// An admin connects for the company and disconnects.
	adminCookie := session(admin)
	rec, body := page(adminCookie)
	if !strings.Contains(body, `name="organization" value="acme"`) || !strings.Contains(body, `name="return_to" value="/dashboard/orgs/acme"`) {
		t.Fatalf("admin page lacks the company connect form:\n%s", body)
	}
	match := orgGitHubDisconnectToken.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("admin page lacks the disconnect form:\n%s", body)
	}
	if got := disconnect(adminCookie, rec, match[1]); got.Code != http.StatusSeeOther || !strings.Contains(got.Header().Get("Location"), "github=disconnected") {
		t.Fatalf("admin disconnect = %d %s", got.Code, got.Header().Get("Location"))
	}
	if installs, _ := store.ListGitHubInstallationsForAccount(ctx, org.ID); len(installs) != 0 {
		t.Fatalf("installation still connected: %+v", installs)
	}
	for _, p := range []state.Project{native, migrated} {
		after, err := store.ProjectByOrgSlug(ctx, org.ID, p.Slug)
		if err != nil || after.InstallID != 0 {
			t.Fatalf("project %s still bound after disconnect: %+v %v", p.Slug, after, err)
		}
	}
}
