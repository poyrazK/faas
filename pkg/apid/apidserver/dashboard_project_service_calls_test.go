package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectPageRow returns the project page's table row for an app slug.
func projectPageRow(t *testing.T, body, slug string) string {
	t.Helper()
	start := strings.Index(body, `<tr><td><a href="/dashboard/apps/`+slug+`">`)
	if start < 0 {
		t.Fatalf("no row for %s:\n%s", slug, body)
	}
	end := strings.Index(body[start:], "</tr>")
	if end < 0 {
		t.Fatalf("unterminated row for %s", slug)
	}
	return body[start : start+end]
}

// The project page shows which service each service calls and is called by,
// from the declared calls, matched by service name (ADR-20261026170512).
func TestDashboardProjectPageListsServiceCalls(t *testing.T) {
	srv, store, account, project, _ := apidserver.NewProjectLifecycleFixture(t)
	manifest := state.AppManifest{ServiceBindings: []api.AppServiceBinding{{Service: "api", Binding: "GREGALE_SERVICE_API_URL"}}}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web",
		Type: state.AppTypeApp, Status: state.AppActive, Manifest: manifest}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/projects/shop", nil)
	rec := httptest.NewRecorder()
	srv.RenderProjectDetail(rec, req, account, "shop")
	if rec.Code != http.StatusOK {
		t.Fatalf("project page status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<th>Calls</th><th>Called by</th>") {
		t.Fatalf("project page has no call columns:\n%s", body)
	}
	if web := projectPageRow(t, body, "shop-web"); !strings.HasSuffix(web, "<td><code>api</code></td><td>—</td>") {
		t.Fatalf("web row = %s; want it calling api and called by nothing", web)
	}
	if apiRow := projectPageRow(t, body, "shop-api"); !strings.HasSuffix(apiRow, "<td>—</td><td><code>web</code></td>") {
		t.Fatalf("api row = %s; want it called by web", apiRow)
	}
	if strings.Contains(body, "Service calls, last") {
		t.Fatal("measured call health shown without a released service map")
	}
}

// The project page's measured calls are those between the project's own
// services; a call from or to another app stays on the account's map.
func TestProjectServiceCallsKeepsTheProjectsOwnEdges(t *testing.T) {
	workloads := []api.ProjectWorkloadResponse{{Slug: "shop-web"}, {Slug: "shop-api"}}
	edges := []api.ServiceMapEdge{
		{CallerAppSlug: "shop-web", TargetAppSlug: "shop-api", Calls: 10},
		{CallerAppSlug: "shop-web", TargetAppSlug: "billing", Calls: 4},
		{CallerAppSlug: "reports", TargetAppSlug: "shop-api", Calls: 2},
	}
	got := apidserver.ProjectServiceCalls(edges, workloads)
	if len(got) != 1 || got[0].CallerAppSlug != "shop-web" || got[0].TargetAppSlug != "shop-api" {
		t.Fatalf("project edges = %+v; want only shop-web -> shop-api", got)
	}
}
