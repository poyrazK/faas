package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type scopedDiagnosticTestStore struct {
	*state.MemStore
	scope, env   string
	calls        int
	failIdentity bool
}

func (s *scopedDiagnosticTestStore) check(tScope, tEnv string) {
	s.scope = tScope
	s.env = tEnv
	s.calls++
}
func (s *scopedDiagnosticTestStore) RouteAwakeMinutes(_ context.Context, p sqlc.RouteAwakeMinutesParams) ([]sqlc.RouteAwakeMinutesRow, error) {
	s.check(p.SelectedScope, p.SelectedEnvironmentID)
	return []sqlc.RouteAwakeMinutesRow{{Minute: pgtype.Timestamptz{Time: time.Now().UTC().Truncate(time.Minute).Add(-time.Minute), Valid: true}, MbSeconds: 1000}}, nil
}
func (s *scopedDiagnosticTestStore) RouteAwakeRouteMinutes(_ context.Context, p sqlc.RouteAwakeRouteMinutesParams) ([]sqlc.RouteAwakeRouteMinutesRow, error) {
	s.check(p.SelectedScope, p.SelectedEnvironmentID)
	return []sqlc.RouteAwakeRouteMinutesRow{}, nil
}
func (s *scopedDiagnosticTestStore) RouteAdviceRouteStats(_ context.Context, p sqlc.RouteAdviceRouteStatsParams) ([]sqlc.RouteAdviceRouteStatsRow, error) {
	s.check(p.SelectedScope, p.SelectedEnvironmentID)
	return []sqlc.RouteAdviceRouteStatsRow{{Route: "GET /catalog", Method: "GET", Requests: 1000, AnonymousRequests: 1000, AnonymousSuccess: 1000, CacheHits: 900, ContentFirst: 10, ContentSame60: 900}}, nil
}
func (s *scopedDiagnosticTestStore) RouteAdviceConsumers(_ context.Context, p sqlc.RouteAdviceConsumersParams) ([]sqlc.RouteAdviceConsumersRow, error) {
	s.check(p.SelectedScope, p.SelectedEnvironmentID)
	return nil, nil
}
func (s *scopedDiagnosticTestStore) RouteAdviceThrottleExcess(_ context.Context, p sqlc.RouteAdviceThrottleExcessParams) (int64, error) {
	s.check(p.SelectedScope, p.SelectedEnvironmentID)
	return 0, nil
}
func (s *scopedDiagnosticTestStore) ProjectEnvironmentBySlug(ctx context.Context, acct, project, slug string) (state.ProjectEnvironment, error) {
	env, err := s.MemStore.ProjectEnvironmentBySlug(ctx, acct, project, slug)
	if s.failIdentity && s.calls > 0 {
		env.ID = "replacement"
	}
	return env, err
}

func TestScopedRouteDiagnosticsHTTP(t *testing.T) {
	e := setup(t, api.PlanPro)
	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "diagnostics"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "diagnostic-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	env, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	store := &scopedDiagnosticTestStore{MemStore: e.store}
	e.s.store = store
	for _, path := range []string{"awake", "advice"} {
		for _, selector := range []struct{ query, scope, env string }{{"scope=default", "default", ""}, {"environment=staging", "staging", env.ID}} {
			r := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/routes/"+path+"?since=1h&"+selector.query, nil, nil)
			var identity api.RouteDiagnosticsIdentity
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &identity) != nil || identity.AppID != app.ID || identity.Scope != selector.scope || identity.EnvironmentID != selector.env || identity.Coverage != "retained_deployment_history" || !identity.Partial {
				t.Fatalf("scoped report %s: %d %s", path, r.Code, r.Body)
			}
			if store.scope != selector.scope || store.env != selector.env {
				t.Fatal("scope did not reach telemetry reader")
			}
			if path == "advice" {
				var out api.RouteAdviceResponse
				if json.Unmarshal(r.Body.Bytes(), &out) != nil || out.CacheabilityCoverage != "deployment_isolated_observations_only" || len(out.Suggestions) == 0 || out.PolicyContext == nil || out.PolicyContext.Status != "observed" {
					t.Fatalf("missing isolated content evidence: %s", r.Body)
				}
				for _, suggestion := range out.Suggestions {
					if len(suggestion.Rules) != 0 || suggestion.Setting != nil || suggestion.Policy == nil || suggestion.Policy.Status != "no_matching_rule" {
						t.Fatal("scoped diagnosis borrowed mutation recipes")
					}
				}
			}
		}
		for _, query := range []string{"scope=all", "scope=default&environment=staging", "environment=staging&environment=production", "scope=default&since=1h&since=2h", "environment=missing", "scope=", "unknown=1", "environment=%ZZ", "environment=%20staging"} {
			before := store.calls
			r := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/routes/"+path+"?"+query, nil, nil)
			if r.Code == 200 || store.calls != before {
				t.Fatalf("invalid selector reached telemetry: %s %d", query, r.Code)
			}
		}
	}
	store.calls = 0
	store.failIdentity = true
	r := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/routes/awake?environment=staging", nil, nil)
	if r.Code != 409 {
		t.Fatalf("environment identity race accepted: %d %s", r.Code, r.Body)
	}
}
