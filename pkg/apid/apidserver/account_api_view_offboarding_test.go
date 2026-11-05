package apidserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// The account-wide API views read per-app rows without naming an app in
// their URL, so appaccess.Wrap never decides for them: the latest deployment
// per app, a trace lookup, the service map and the event stream. Each shows
// a company app to its creator while a member and drops it once the company
// removes them; the creator's own app stays.
func TestAccountAPIViewsHideCompanyAppsAfterOffboarding(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, srv, creator := *e.Store(), *e.S(), *e.Acct()
	ctx := t.Context()
	owner, err := store.CreateAccount(ctx, "acme-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	deployments := map[string]string{}
	newApp := func(app state.App) state.App {
		t.Helper()
		app.AccountID, app.Type, app.Runtime, app.RAMMB, app.Status = creator.ID, state.AppTypeApp, "node22", 128, state.AppActive
		created, err := store.CreateApp(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		app = created
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:" + app.Slug,
			Kind: state.DeploymentKindImage, Status: state.DeployLive, CreatedAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		deployments[app.Slug] = dep.ID
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: creator.ID, Source: state.InvocationQueue,
			Headers: json.RawMessage(`{"X-Gregale-Trace-Id":"` + traceID + `","traceparent":"00-` + traceID + `-00f067aa0ba902b7-01"}`), DueAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		return app
	}
	company := newApp(state.App{OrgID: org.ID, Slug: "acme-api"})
	own := newApp(state.App{Slug: "own-api"})

	// shown reports which of the two apps each view returns.
	shown := func() map[string]map[string]bool {
		t.Helper()
		views := map[string]map[string]bool{"latest-by-app": {}, "trace": {}, "service-map": {}, "events": {}}

		var latest struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		rec := e.Do(t, http.MethodGet, "/v1/deployments/latest-by-app", nil, nil)
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &latest) != nil {
			t.Fatalf("latest-by-app = %d %s", rec.Code, rec.Body.String())
		}
		for _, item := range latest.Items {
			for slug, id := range deployments {
				views["latest-by-app"][slug] = views["latest-by-app"][slug] || item.ID == id
			}
		}

		var trace api.AccountTraceLookupResponse
		rec = e.Do(t, http.MethodGet, "/v1/account/traces/"+traceID, nil, nil)
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &trace) != nil {
			t.Fatalf("trace lookup = %d %s", rec.Code, rec.Body.String())
		}
		for _, inv := range trace.Invocations {
			views["trace"][inv.App] = true
		}

		apps, err := srv.ServiceMapAppsForTest(middleware.WithPrincipal(ctx, creator, nil, nil), creator.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, app := range apps {
			views["service-map"][app.Slug] = true
		}

		owned, hidden := srv.EventsAppScopeForTest(ctx, creator, nil)
		for _, app := range []state.App{company, own} {
			frame := db.Notification{Channel: db.NotifyDeploymentChanged,
				Payload: fmt.Sprintf(`{"account_id":%q,"app_id":%q}`, creator.ID, app.ID)}
			_, views["events"][app.Slug] = apidserver.NormalizedEventsFrameForAccount(frame, creator.ID, owned, hidden)
		}
		return views
	}

	for view, apps := range shown() {
		if !apps[company.Slug] || !apps[own.Slug] {
			t.Fatalf("member %s shows %v, want both apps", view, apps)
		}
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	for view, apps := range shown() {
		if apps[company.Slug] {
			t.Errorf("offboarded creator's %s still shows the company app", view)
		}
		if !apps[own.Slug] {
			t.Errorf("offboarded creator's %s dropped their own app", view)
		}
	}
}
