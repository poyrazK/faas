package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func changeTimelineEnv(t *testing.T) testEnv {
	t.Helper()
	e := setup(t, api.PlanHobby)
	e.s.changeTimelineEnabled = true
	return e
}

func decodeChangeTimeline(t *testing.T, rec *httptest.ResponseRecorder) api.AppChangeTimelineResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppChangeTimelineResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestGetChangeTimeline_DisabledReturns503(t *testing.T) {
	e := setup(t, api.PlanHobby)
	createApp(t, e, "shop")
	rec := e.do(t, http.MethodGet, "/v1/apps/shop/changes", nil, nil)
	assertProblem(t, rec, http.StatusServiceUnavailable, "change_timeline_unavailable")
}

func TestGetChangeTimeline_RejectsInvalidWindows(t *testing.T) {
	e := changeTimelineEnv(t)
	createApp(t, e, "shop")
	for _, query := range []string{
		"since=yesterday",
		"until=2026-13-01T00:00:00Z",
		"since=2026-10-02T00:00:00Z&until=2026-10-01T00:00:00Z",
		"since=2026-09-01T00:00:00Z&until=2026-10-01T00:00:00Z",
	} {
		rec := e.do(t, http.MethodGet, "/v1/apps/shop/changes?"+query, nil, nil)
		assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	}
}

func TestGetChangeTimeline_ForeignAppIsNotFound(t *testing.T) {
	e := changeTimelineEnv(t)
	foreign, _ := mustCreateAccount(t, e.store, "change-timeline-foreign", api.PlanHobby)
	mustSeedAppFor(t, e.store, foreign.ID, "foreign-shop")
	rec := e.do(t, http.MethodGet, "/v1/apps/foreign-shop/changes", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign app = %d, want 404: %s", rec.Code, rec.Body)
	}
}

// TestGetChangeTimeline_MergesSources is the capability acceptance test
// (pkg/productcap/catalog.json, ADR-741): deployment audit, runtime config,
// and org activity merge newest first; deploy activity and other apps'
// rows stay out; summaries carry names, never values.
func TestGetChangeTimeline_MergesSources(t *testing.T) {
	e := changeTimelineEnv(t)
	ctx := context.Background()
	app := createApp(t, e, "shop")
	other := createApp(t, e, "other")
	now := time.Now().UTC()

	dep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeploymentStatus("active")})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	otherDep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: other.ID, Status: state.DeploymentStatus("active")})
	if err != nil {
		t.Fatalf("create other deployment: %v", err)
	}
	for _, row := range []struct {
		dep  string
		kind state.DeploymentAuditKind
		at   time.Time
	}{
		{dep.ID, "deploy.created", now.Add(-3 * time.Hour)},
		{dep.ID, "deploy.rolled_back", now.Add(-1 * time.Hour)},
		{dep.ID, "deploy.created", now.Add(-48 * time.Hour)},
		{otherDep.ID, "deploy.created", now.Add(-2 * time.Hour)},
	} {
		if _, err := e.store.AppendDeploymentAudit(ctx, state.DeploymentAudit{DeploymentID: uuid.MustParse(row.dep), Kind: row.kind, Actor: "system", At: row.at}); err != nil {
			t.Fatalf("append audit: %v", err)
		}
	}
	if err := e.store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatalf("mark runtime config: %v", err)
	}
	if app.OrgID == "" {
		t.Fatal("test app has no owning org; org activity cannot be seeded")
	}
	appID := uuid.MustParse(app.ID)
	for i, kind := range []string{"env.set", "deploy.requested"} {
		if _, err := e.store.AppendOrgActivity(ctx, state.OrgActivity{
			OrgID: uuid.MustParse(app.OrgID), OccurredAt: now.Add(-30 * time.Minute), Kind: kind,
			ActorType: state.OrgActivityActorUser, ActorLabel: "dev@example.com",
			ResourceType: "environment_variable", ResourceID: "app:DATABASE_URL", ResourceLabel: "DATABASE_URL",
			AppID: &appID, SourceType: kind, SourceID: string(rune('a' + i)),
		}); err != nil {
			t.Fatalf("append activity %s: %v", kind, err)
		}
	}

	out := decodeChangeTimeline(t, e.do(t, http.MethodGet, "/v1/apps/shop/changes", nil, nil))
	if len(out.UnavailableSources) != 0 || out.Truncated {
		t.Fatalf("unavailable=%v truncated=%v, want neither", out.UnavailableSources, out.Truncated)
	}
	var kinds []string
	for i, ev := range out.Events {
		kinds = append(kinds, ev.Kind)
		if i > 0 && ev.At.After(out.Events[i-1].At) {
			t.Fatalf("events not newest first: %+v", out.Events)
		}
		if strings.Contains(ev.Summary, "dev@example.com") {
			t.Fatalf("summary leaked the actor: %q", ev.Summary)
		}
	}
	if got := strings.Join(kinds, ","); got != "runtime_config.changed,env.set,deploy.rolled_back,deploy.created" {
		t.Fatalf("kinds = %s, want runtime config, env.set, rollback, create (48h-old and foreign rows excluded)", got)
	}
	if out.Events[1].Summary != "Environment variable DATABASE_URL set" {
		t.Fatalf("activity summary = %q", out.Events[1].Summary)
	}
	if out.Events[2].DeploymentID == "" || !strings.HasPrefix(out.Events[2].Summary, "Rollback recorded for deployment ") {
		t.Fatalf("deployment event = %+v", out.Events[2])
	}

	wide := decodeChangeTimeline(t, e.do(t, http.MethodGet, "/v1/apps/shop/changes?since="+now.Add(-72*time.Hour).Format(time.RFC3339), nil, nil))
	if len(wide.Events) != len(out.Events)+1 {
		t.Fatalf("72h window has %d events, want %d (the 48h-old create)", len(wide.Events), len(out.Events)+1)
	}
}

type failingDeploymentAuditStore struct{ *state.MemStore }

func (failingDeploymentAuditStore) ListAppDeploymentAuditBetween(context.Context, string, string, time.Time, time.Time, int) ([]state.AppChangeRow, error) {
	return nil, errors.New("database unavailable")
}

func TestGetChangeTimeline_ReportsUnavailableSource(t *testing.T) {
	e := changeTimelineEnv(t)
	app := createApp(t, e, "shop")
	if err := e.store.MarkAppRuntimeConfigChanged(context.Background(), app.ID); err != nil {
		t.Fatalf("mark runtime config: %v", err)
	}
	e.s.store = failingDeploymentAuditStore{e.store}

	out := decodeChangeTimeline(t, e.do(t, http.MethodGet, "/v1/apps/shop/changes", nil, nil))
	if len(out.UnavailableSources) != 1 || out.UnavailableSources[0] != api.ChangeSourceDeployment {
		t.Fatalf("unavailable_sources = %v, want [deployment]", out.UnavailableSources)
	}
	if len(out.Events) != 1 || out.Events[0].Source != api.ChangeSourceRuntimeConfig {
		t.Fatalf("events = %+v, want the runtime config change from the healthy source", out.Events)
	}
}

func TestAppChangeTimeline_CapsNewestEvents(t *testing.T) {
	e := changeTimelineEnv(t)
	ctx := context.Background()
	app := createApp(t, e, "shop")
	dep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeploymentStatus("active")})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < api.ChangeTimelineMaxEvents+5; i++ {
		if _, err := e.store.AppendDeploymentAudit(ctx, state.DeploymentAudit{DeploymentID: uuid.MustParse(dep.ID), Kind: "deploy.traffic_changed", Actor: "system", At: now.Add(-time.Duration(i+1) * time.Minute)}); err != nil {
			t.Fatalf("append audit: %v", err)
		}
	}
	out := e.s.appChangeTimeline(ctx, e.acct, app, now.Add(-24*time.Hour), now, now)
	if !out.Truncated || len(out.Events) != api.ChangeTimelineMaxEvents {
		t.Fatalf("events=%d truncated=%v, want %d and truncated", len(out.Events), out.Truncated, api.ChangeTimelineMaxEvents)
	}
	if !out.Events[0].At.Equal(now.Add(-time.Minute)) {
		t.Fatalf("newest event = %v, want the most recent row kept", out.Events[0].At)
	}
}

func renderAppChangesPage(t *testing.T, e testEnv, slug, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/"+slug+"/changes"+query, nil)
	r.SetPathValue("slug", slug)
	r = r.WithContext(WithAccount(r.Context(), e.acct))
	rec := httptest.NewRecorder()
	e.s.renderAppChangesDashboard(rec, r)
	return rec
}

func TestAppChangesDashboard_RendersMarkersAndTable(t *testing.T) {
	e := changeTimelineEnv(t)
	ctx := context.Background()
	app := createApp(t, e, "shop")
	dep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeploymentStatus("active")})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if _, err := e.store.AppendDeploymentAudit(ctx, state.DeploymentAudit{DeploymentID: uuid.MustParse(dep.ID), Kind: "deploy.rolled_back", Actor: "system", At: time.Now().UTC().Add(-2 * time.Hour)}); err != nil {
		t.Fatalf("append audit: %v", err)
	}

	rec := renderAppChangesPage(t, e, "shop", "?range=7d")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<strong>7d</strong>",
		"Metrics are unavailable right now",
		`<line x1=`,
		"Rollback recorded for deployment " + shortChangeID(dep.ID),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestAppChangesDashboard_DisabledAndForeign(t *testing.T) {
	e := setup(t, api.PlanHobby)
	createApp(t, e, "shop")
	if body := renderAppChangesPage(t, e, "shop", "").Body.String(); !strings.Contains(body, "not enabled for this deployment") {
		t.Fatalf("disabled page should say so:\n%s", body)
	}
	foreign, _ := mustCreateAccount(t, e.store, "changes-dashboard-foreign", api.PlanHobby)
	mustSeedAppFor(t, e.store, foreign.ID, "foreign-shop")
	if rec := renderAppChangesPage(t, e, "foreign-shop", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign app page = %d, want 404", rec.Code)
	}
}

// adr: 741 — the dashboard route sits behind the session chain like its peers.
func TestAppChangesDashboard_RequiresSession(t *testing.T) {
	e := changeTimelineEnv(t)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/apps/shop/changes", nil))
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), loginPath) {
		t.Fatalf("unauthenticated GET = %d %q, want redirect to %s", rec.Code, rec.Header().Get("Location"), loginPath)
	}
}
