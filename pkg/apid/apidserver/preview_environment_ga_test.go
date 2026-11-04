// adr: 20261026090000 — PR preview environments are destroyed as a whole
// and admitted by environment.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedPRPreview(t *testing.T, store *state.MemStore, accountID, projectID, slug, parent string, pr int) state.App {
	t.Helper()
	expires := time.Now().Add(time.Hour)
	app, err := store.CreateAppIfUnderQuota(context.Background(), state.App{AccountID: accountID, ProjectID: projectID,
		Slug: slug, WorkloadName: parent, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive,
		PreviewOfSlug: parent, PreviewPrNumber: pr, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires,
	}, api.Limits{DeployedApps: 100, PreviewApps: 100, DeveloperApps: 100})
	if err != nil {
		t.Fatalf("seed %s: %v", slug, err)
	}
	return app
}

func previewEnvironmentFixture(t *testing.T, e apidserver.TestEnv) (root, sibling, otherPR state.App) {
	t.Helper()
	store := *e.Store()
	project, err := store.CreateProject(context.Background(), state.Project{AccountID: (*e.Acct()).ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	account := (*e.Acct()).ID
	root = seedPRPreview(t, store, account, project.ID, "pr-42-web", "web", 42)
	sibling = seedPRPreview(t, store, account, project.ID, "pr-42-ledger", "ledger", 42)
	otherPR = seedPRPreview(t, store, account, project.ID, "pr-43-web", "web", 43)
	return root, sibling, otherPR
}

func requireTornDown(t *testing.T, store *state.MemStore, ids ...string) {
	t.Helper()
	for _, id := range ids {
		app, err := store.AppByID(context.Background(), id)
		if err != nil || app.Status != state.AppDeleted || app.PreviewPrState != state.PreviewPrStateTornDown {
			t.Fatalf("app %s = (%s/%s, %v), want torn down", id, app.Status, app.PreviewPrState, err)
		}
	}
}

func requireActive(t *testing.T, store *state.MemStore, id string) {
	t.Helper()
	if app, err := store.AppByID(context.Background(), id); err != nil || app.Status != state.AppActive {
		t.Fatalf("app %s = (%+v, %v), want untouched", id, app, err)
	}
}

func TestDestroyPreviewTearsDownItsPREnvironment(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	root, sibling, otherPR := previewEnvironmentFixture(t, e)
	rec := e.Do(t, http.MethodPost, "/v1/preview/pr-42-web/destroy", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("destroy = %d %s", rec.Code, rec.Body)
	}
	requireTornDown(t, *e.Store(), root.ID, sibling.ID)
	requireActive(t, *e.Store(), otherPR.ID)
}

func TestDashboardPreviewTearDownFormDestroysTheEnvironment(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	root, sibling, otherPR := previewEnvironmentFixture(t, e)
	session := apidserver.RestartProgressDashboardCookie(t, e, (*e.Acct()).ID, false)
	page := apidserver.RestartProgressDashboardGET(e, session, "/dashboard/previews")
	token := apidserver.DashboardInputToken(page.Body.String())
	csrf := apidserver.FindDashboardCookie(page.Result().Cookies(), apidserver.DashboardPreviewDestroyCSRFCookie)
	if page.Code != http.StatusOK || token == "" || csrf == nil ||
		!strings.Contains(page.Body.String(), `action="/dashboard/apps/ledger/preview/pr-42-ledger/destroy"`) {
		t.Fatalf("previews page = %d token=%q csrf=%v\n%s", page.Code, token, csrf, page.Body)
	}
	post := func(path string, form url.Values, withCSRF bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(session)
		if withCSRF {
			r.AddCookie(csrf)
		}
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	if rec := post("/dashboard/apps/ledger/preview/pr-42-ledger/destroy", url.Values{"csrf_token": {token}}, false); rec.Code == http.StatusSeeOther {
		t.Fatal("tear down without the CSRF cookie was accepted")
	}
	if rec := post("/dashboard/apps/web/preview/pr-42-ledger/destroy", url.Values{"csrf_token": {token}}, true); rec.Code != http.StatusNotFound {
		t.Fatalf("mismatched parent = %d, want 404", rec.Code)
	}
	requireActive(t, *e.Store(), sibling.ID)
	rec := post("/dashboard/apps/ledger/preview/pr-42-ledger/destroy", url.Values{"csrf_token": {token}}, true)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard/previews?destroyed=pr-42-ledger" {
		t.Fatalf("tear down = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	requireTornDown(t, *e.Store(), root.ID, sibling.ID)
	requireActive(t, *e.Store(), otherPR.ID)
	if after := apidserver.RestartProgressDashboardGET(e, session, "/dashboard/previews?destroyed=pr-42-ledger"); !strings.Contains(after.Body.String(), "were torn down") {
		t.Fatalf("previews page after tear down lacks the outcome:\n%s", after.Body)
	}
}

func TestCreatePreviewAdmitsByEnvironment(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	store := *e.Store()
	if _, err := store.CreateAppIfUnderQuota(context.Background(), state.App{AccountID: (*e.Acct()).ID, Slug: "acme",
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, Status: state.AppActive}, api.MustLimitsFor(api.PlanFree)); err != nil {
		t.Fatal(err)
	}
	create := func(pr int) *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/apps/acme/previews", api.CreatePreviewRequest{PRNumber: pr}, nil)
	}
	if rec := create(42); rec.Code != http.StatusCreated {
		t.Fatalf("first preview = %d %s", rec.Code, rec.Body)
	}
	rec := create(43)
	var problem api.Problem
	_ = json.NewDecoder(rec.Body).Decode(&problem)
	if rec.Code != http.StatusForbidden || problem.Title != "PR preview limit reached" {
		t.Fatalf("second open preview = %d %+v, want the environment allowance", rec.Code, problem)
	}
	first, err := store.AppBySlug(context.Background(), "pr-42-acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClosePRPreview(context.Background(), first.ID, time.Now().Add(state.PRPreviewClosedGrace)); err != nil {
		t.Fatal(err)
	}
	if rec := create(43); rec.Code != http.StatusCreated {
		t.Fatalf("preview after the first PR closed = %d %s, want the closed preview's environment free", rec.Code, rec.Body)
	}
	if rec := create(42); rec.Code != http.StatusForbidden {
		t.Fatalf("reopening the closed preview beside an open one = %d %s, want 403", rec.Code, rec.Body)
	}
}

// TestCreatePreviewDoesNotCopyProductionEnv pins ADR-310 on the developer
// preview API: the preview runs unpromoted code, so only platform-owned
// service binding URLs reach it, never the production app's env values.
func TestCreatePreviewDoesNotCopyProductionEnv(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	if _, err := store.CreateAppIfUnderQuota(context.Background(), state.App{AccountID: (*e.Acct()).ID, Slug: "acme",
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, Status: state.AppActive,
		Manifest: state.AppManifest{
			Env:             map[string]string{"DATABASE_URL": "postgres://prod", "GREGALE_SERVICE_BILLING_URL": "http://stale"},
			ServiceBindings: []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_BILLING_URL", Service: "billing"}},
		},
	}, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPost, "/v1/apps/acme/previews", api.CreatePreviewRequest{PRNumber: 7}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create preview = %d %s", rec.Code, rec.Body)
	}
	preview, err := store.AppBySlug(context.Background(), "pr-7-acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := preview.Manifest.Env["DATABASE_URL"]; leaked {
		t.Fatalf("preview env = %#v, copied the production value", preview.Manifest.Env)
	}
	if got := preview.Manifest.Env["GREGALE_SERVICE_BILLING_URL"]; got != "http://billing.svc.gregale:10081" {
		t.Fatalf("preview binding URL = %q, want the platform-owned service URL", got)
	}
}

// adr: 20261028100000 — a preview of a 40-character app is created (its slug
// is longer than an app slug may be) and does not keep production's warm
// instances.
func TestCreatePreviewOfLongWarmApp(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	parent := "a" + strings.Repeat("b", 38) + "c"
	if _, err := store.CreateAppIfUnderQuota(context.Background(), state.App{AccountID: (*e.Acct()).ID, Slug: parent,
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, MinInstances: 2, Status: state.AppActive},
		api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPost, "/v1/apps/"+parent+"/previews", api.CreatePreviewRequest{PRNumber: 12}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create preview = %d %s", rec.Code, rec.Body)
	}
	preview, err := store.AppBySlug(context.Background(), "pr-12-"+parent)
	if err != nil {
		t.Fatal(err)
	}
	if preview.MinInstances != 0 {
		t.Fatalf("preview min instances = %d, want 0 (previews scale to zero)", preview.MinInstances)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/apps/pr-12-"+parent, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("read long preview = %d %s", rec.Code, rec.Body)
	}
}
