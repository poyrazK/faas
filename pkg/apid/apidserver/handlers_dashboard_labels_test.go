package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

type labelsDashboard struct {
	srv    *apidserver.Server
	cookie *http.Cookie
	store  *state.MemStore
}

func (d labelsDashboard) get(t *testing.T, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.AddCookie(d.cookie)
	d.srv.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d\n%s", path, rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// newLabelsDashboard seeds three labelled apps and a labelled cron.
func newLabelsDashboard(t *testing.T) labelsDashboard {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "labels@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := apidserver.MintDashboardSession(ctx, store, mgr, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	for slug, set := range map[string]map[string]string{
		"billing-web": {"team": "billing", "tier": "web"},
		"billing-api": {"team": "billing", "tier": "api"},
		"search-web":  {"team": "search"},
	} {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateAppLabels(ctx, app.ID, labels.Change{Set: set}); err != nil {
			t.Fatal(err)
		}
		if slug != "billing-web" {
			continue
		}
		cron, err := store.CreateCron(ctx, app.ID, "0 * * * *", "/report", true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateResourceLabels(ctx, state.LabelKindCron, cron.ID, labels.Change{Set: map[string]string{"owner": "finance"}}); err != nil {
			t.Fatal(err)
		}
	}
	srv := apidserver.NewServerWithDeps(store, apidserver.DiscardLogger(), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*60_000_000_000, "")
	return labelsDashboard{srv: srv, cookie: &http.Cookie{Name: apidserver.SessionCookie, Value: sid}, store: store}
}

func appsPage(selector string) string {
	if selector == "" {
		return "/dashboard/apps"
	}
	return "/dashboard/apps?" + url.Values{"selector": {selector}}.Encode()
}

// The apps page shows labels and filters by a selector, and the app page
// shows the app's and its crons' labels (ADR-20261031100025).
func TestDashboardLabels(t *testing.T) {
	d := newLabelsDashboard(t)

	body := d.get(t, appsPage(""))
	for _, want := range []string{`name="selector"`, "<th>Labels</th>", "team=billing", "tier=api", "team=search", "billing-web", "search-web"} {
		if !strings.Contains(body, want) {
			t.Errorf("apps page missing %q", want)
		}
	}

	for _, tc := range []struct {
		selector    string
		want, avoid []string
	}{
		{selector: "team=billing,tier notin (api)", want: []string{"<code>billing-web</code>", `hx-get="/dashboard/apps?selector=`, ">Clear<"}, avoid: []string{"<code>billing-api</code>", "<code>search-web</code>"}},
		{selector: "team=nobody", want: []string{"No app matches"}, avoid: []string{"<code>billing-web</code>", "Deploy your first app"}},
		{selector: "tier in (web", want: []string{`role="alert"`}, avoid: []string{"<code>billing-web</code>", "No app matches", "Deploy your first app"}},
	} {
		body := d.get(t, appsPage(tc.selector))
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Errorf("%q: page missing %q", tc.selector, want)
			}
		}
		for _, avoid := range tc.avoid {
			if strings.Contains(body, avoid) {
				t.Errorf("%q: page shows %q", tc.selector, avoid)
			}
		}
	}

	detail := d.get(t, "/dashboard/apps/billing-web")
	for _, want := range []string{`aria-label="Labels"`, "team=billing", "owner=finance"} {
		if !strings.Contains(detail, want) {
			t.Errorf("app page missing %q", want)
		}
	}
}

// While the account does not have resource labels the pages show none, and
// the apps page ignores a selector instead of hiding apps.
func TestDashboardLabels_CapabilityOff(t *testing.T) {
	d := newLabelsDashboard(t)
	if err := d.srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}

	body := d.get(t, appsPage("team=search"))
	for _, avoid := range []string{`name="selector"`, "<th>Labels</th>", "team=billing"} {
		if strings.Contains(body, avoid) {
			t.Errorf("apps page with labels off shows %q", avoid)
		}
	}
	if !strings.Contains(body, "<code>billing-web</code>") {
		t.Error("apps page with labels off hid an app for a selector")
	}
	detail := d.get(t, "/dashboard/apps/billing-web")
	if strings.Contains(detail, "team=billing") || strings.Contains(detail, "owner=finance") {
		t.Error("app page with labels off shows labels")
	}

	d.srv.SeedPlatformFlag(capabilityFlag(t, "resource-labels"), platformflags.Definition{Enabled: true, Default: true})
	if body := d.get(t, appsPage("")); !strings.Contains(body, "team=billing") {
		t.Error("apps page shows no labels once the capability is on")
	}
}
