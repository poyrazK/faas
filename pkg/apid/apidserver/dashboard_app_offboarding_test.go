package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDashboardHidesCompanyAppsAfterOffboarding covers the dashboard pages
// that read a company app without passing the {slug} route gate: the app
// pages the "GET /dashboard/" subtree dispatches by path, the account-wide
// jobs, apps, previews and MCP pages, an async invocation's detail, and the
// MCP actions under /dashboard/mcp/apps/{slug}. Each answers the creator
// while a member and stops once the company removes them; the creator's own
// app is unaffected.
func TestDashboardHidesCompanyAppsAfterOffboarding(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	ctx := t.Context()
	owner, err := env.store.CreateAccount(ctx, "acme-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := env.store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := env.store.AddOrgMember(ctx, org.ID, env.account.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	newApp := func(app state.App) state.App {
		t.Helper()
		app.AccountID, app.Type, app.Runtime, app.RAMMB, app.Status = env.account.ID, state.AppTypeApp, "node22", 128, state.AppActive
		created, err := env.store.CreateApp(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	company := newApp(state.App{OrgID: org.ID, Slug: "acme-api"})
	own := newApp(state.App{Slug: "own-api"})
	newApp(state.App{OrgID: org.ID, Slug: "pr-7-acme-api", PreviewOfSlug: company.Slug, PreviewPrNumber: 7})
	enqueue := func(app state.App, source state.InvocationSource, marker string) state.Invocation {
		t.Helper()
		inv, err := env.store.EnqueueInvocation(ctx, state.Invocation{AccountID: env.account.ID, AppID: app.ID, Source: source,
			State: state.InvocationPending, DueAt: time.Now().UTC(), Method: http.MethodPost, Path: "/" + marker,
			Payload: json.RawMessage(`{"marker":"` + marker + `"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	enqueue(company, state.InvocationQueue, "acmequeuepayload")
	enqueue(own, state.InvocationQueue, "ownqueuepayload")
	companyAsync := enqueue(company, state.InvocationAsyncInvoke, "acmeasyncpath")

	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(url.Values{}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(env.session)
		env.h.ServeHTTP(rec, req)
		return rec
	}
	pages := []string{
		"/dashboard/apps/acme-api", "/dashboard/apps/acme-api/logs", "/dashboard/apps/acme-api/env",
		"/dashboard/apps/acme-api/debug", "/dashboard/apps/acme-api/jobs", "/dashboard/apps/acme-api/queues",
		"/dashboard/jobs?app=acme-api", "/dashboard/mcp?app=acme-api", "/dashboard/invocations/" + companyAsync.ID,
	}
	listings := map[string][]string{
		"/dashboard/jobs":     {company.Slug, "acmequeuepayload", "acmeasyncpath"},
		"/dashboard/apps":     {company.Slug},
		"/dashboard/previews": {"pr-7-acme-api"},
		"/dashboard/mcp":      {company.Slug},
	}
	const mcpAlert = "/dashboard/mcp/apps/acme-api/alerts"

	for _, path := range pages {
		if rec := do(http.MethodGet, path); rec.Code != http.StatusOK {
			t.Fatalf("member GET %s = %d, want 200", path, rec.Code)
		}
	}
	for path, wants := range listings {
		body := do(http.MethodGet, path).Body.String()
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Fatalf("member GET %s does not show %q", path, want)
			}
		}
	}
	if rec := do(http.MethodPost, mcpAlert); rec.Code == http.StatusNotFound {
		t.Fatalf("member POST %s = 404, want it to reach the handler", mcpAlert)
	}

	if err := env.store.RemoveOrgMember(ctx, org.ID, env.account.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range pages {
		if rec := do(http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("offboarded creator GET %s = %d, want 404", path, rec.Code)
		}
	}
	for path, hidden := range listings {
		rec := do(http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Errorf("offboarded creator GET %s = %d, want 200", path, rec.Code)
			continue
		}
		for _, h := range hidden {
			if strings.Contains(rec.Body.String(), h) {
				t.Errorf("offboarded creator GET %s still shows %q", path, h)
			}
		}
	}
	if rec := do(http.MethodPost, mcpAlert); rec.Code != http.StatusNotFound {
		t.Errorf("offboarded creator POST %s = %d, want 404", mcpAlert, rec.Code)
	}
	for _, path := range []string{"/dashboard/apps/own-api", "/dashboard/apps/own-api/jobs", "/dashboard/apps/new"} {
		if rec := do(http.MethodGet, path); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want the creator's own pages to stay", path, rec.Code)
		}
	}
	if body := do(http.MethodGet, "/dashboard/jobs").Body.String(); !strings.Contains(body, "ownqueuepayload") {
		t.Error("the jobs page dropped the creator's own queue")
	}
}
