package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/routepeaks"
	"github.com/onebox-faas/faas/pkg/apid/routesplit"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
)

// Route splits (ADR-20261121170000 amendment 2); the logic is
// pkg/apid/routesplit. The host acts through the same handlers a customer
// calls, so the route's app, its rules and the resize get the API's
// validation, quotas, budget, audit and notifications.

func (s *server) routeSplitHandlers() (routesplit.Handlers, bool) {
	svc, ok := s.routeSplitService()
	if !ok {
		return routesplit.Handlers{}, false
	}
	return routesplit.Handlers{
		Service: svc,
		LoadApp: s.loadApp,
		Enabled: func(acct state.Account) bool {
			return s.platformFlagFor(platformflags.RouteResourcePeaks, routepeaks.FlagSubject(acct.ID, string(acct.Plan)))
		},
	}, true
}

func (s *server) routeSplitService() (*routesplit.Service, bool) {
	store, ok := s.store.(routesplit.Store)
	if !ok {
		return nil, false
	}
	return &routesplit.Service{Store: store, Host: routeSplitHost{s: s}, Log: s.log}, true
}

func (s *server) routeSplitHandler(serve func(routesplit.Handlers, http.ResponseWriter, *http.Request, state.Account)) func(http.ResponseWriter, *http.Request, state.Account) {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		h, ok := s.routeSplitHandlers()
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("route splits"))
			return
		}
		serve(h, w, r, acct)
	}
}

func (s *server) createRouteSplit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.routeSplitHandler(routesplit.Handlers.Create)(w, r, acct)
}

func (s *server) listRouteSplits(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.routeSplitHandler(routesplit.Handlers.List)(w, r, acct)
}

func (s *server) getRouteSplit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.routeSplitHandler(routesplit.Handlers.Get)(w, r, acct)
}

func (s *server) undoRouteSplit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.routeSplitHandler(routesplit.Handlers.Undo)(w, r, acct)
}

type routeSplitHost struct{ s *server }

// ResourceAccount is the account the app's storage and quota belong to.
// Every step acts as it, under a grant for the one app it addresses, so an
// org member's request never acts on an app its own grant does not name.
func (h routeSplitHost) ResourceAccount(ctx context.Context, app state.App) (state.Account, error) {
	return state.AppResourceAccount(ctx, h.s.store, app)
}

func (h routeSplitHost) Suggest(ctx context.Context, _ state.Account, app state.App, window time.Duration) (*routeresources.Split, error) {
	store, ok := h.s.store.(state.RouteResourceStore)
	appID, err := uuid.Parse(app.ID)
	if !ok || err != nil {
		return nil, state.ErrNotFound
	}
	until := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	resp, err := routepeaks.Read(ctx, store, appID, until.Add(-window), until)
	if err != nil {
		return nil, err
	}
	return routepeaks.SplitFor(ctx, h.s.store, app, resp.Report), nil
}

func (h routeSplitHost) CreateApp(ctx context.Context, acct state.Account, source state.App, slug string, ramMB int) (state.App, *api.Problem) {
	req := api.CreateAppRequest{
		Slug: slug, Type: string(source.Type), Runtime: source.Runtime, RAMMB: ramMB,
		CPUMillicores: source.CPUMillicores, IdleTimeoutS: source.IdleTimeoutS, Visibility: string(source.Visibility),
	}
	rec := h.serve(ctx, http.MethodPost, "/v1/apps", req, nil, func(w http.ResponseWriter, r *http.Request) {
		h.s.createAppInOrg(w, r, acct, source.OrgID)
	})
	if prob := recordedProblem(rec, http.StatusCreated); prob != nil {
		return state.App{}, prob
	}
	app, err := h.s.store.AppBySlug(ctx, slug)
	if err != nil {
		return state.App{}, api.ErrCapacity("the route's app was created but could not be read")
	}
	return app, nil
}

func (h routeSplitHost) ResizeApp(ctx context.Context, acct state.Account, app state.App, ramMB int) *api.Problem {
	rec := h.serve(appaccess.WithBindingGrant(ctx, app, acct), http.MethodPatch, "/v1/apps/"+app.Slug, map[string]int{"ram_mb": ramMB}, map[string]string{"slug": app.Slug},
		func(w http.ResponseWriter, r *http.Request) { h.s.updateApp(w, r, acct) })
	return recordedProblem(rec, http.StatusOK)
}

func (h routeSplitHost) DeleteApp(ctx context.Context, acct state.Account, app state.App) *api.Problem {
	rec := h.serve(appaccess.WithBindingGrant(ctx, app, acct), http.MethodDelete, "/v1/apps/"+app.Slug, nil, map[string]string{"slug": app.Slug},
		func(w http.ResponseWriter, r *http.Request) { h.s.deleteApp(w, r, acct) })
	return recordedProblem(rec, http.StatusNoContent, http.StatusOK, http.StatusAccepted)
}

func (h routeSplitHost) AppHosts(ctx context.Context, app state.App) []string {
	hosts := []string{api.AppHostname(app.Slug, h.s.domain)}
	domains, err := h.s.store.ListDomainsForApp(ctx, app.ID)
	if err != nil {
		return hosts
	}
	for _, d := range domains {
		// Environment-bound and wildcard hostnames stay with the app.
		if d.Verified() && d.EnvironmentID == "" && !strings.HasPrefix(d.Domain, "*.") {
			hosts = append(hosts, d.Domain)
		}
	}
	return hosts
}

// DirectHosts is the route's app's platform host and its port-selector hosts
// (<slug>--<port>.<domain>). It has no custom domain: a split never adds one.
func (h routeSplitHost) DirectHosts(_ context.Context, app state.App) []string {
	platform := api.AppHostname(app.Slug, h.s.domain)
	hosts := []string{platform}
	if selector := api.SelectorHostname(platform, "*"); selector != "" {
		hosts = append(hosts, selector)
	}
	return hosts
}

func (h routeSplitHost) RuleKindProblem(acct state.Account, kind string) *api.Problem {
	return h.s.edgeRules.KindProblem(acct, kind)
}

func (h routeSplitHost) CreateRule(ctx context.Context, acct state.Account, app state.App, req api.CreateEdgeRuleRequest) (string, *api.Problem) {
	rule, prob := h.s.edgeRules.CreateRule(ctx, acct, app, req)
	if prob != nil {
		return "", prob
	}
	return rule.ID, nil
}

func (h routeSplitHost) DeleteRule(ctx context.Context, acct state.Account, app state.App, ruleID string) *api.Problem {
	rec := h.serve(appaccess.WithBindingGrant(ctx, app, acct), http.MethodDelete, "/v1/edge-rules/"+ruleID, nil, map[string]string{"id": ruleID},
		func(w http.ResponseWriter, r *http.Request) { h.s.edgeRules.DeleteEdgeRule(w, r, acct) })
	return recordedProblem(rec, http.StatusNoContent)
}

// serve runs one API handler in process, as the dashboard does.
func (h routeSplitHost) serve(ctx context.Context, method, path string, body any, pathValues map[string]string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// recordedProblem is the problem a handler wrote, or nil on a wanted status.
func recordedProblem(rec *httptest.ResponseRecorder, want ...int) *api.Problem {
	for _, status := range want {
		if rec.Code == status {
			return nil
		}
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil || prob.Status == 0 {
		return api.NewProblem(rec.Code, api.CodeCapacity, http.StatusText(rec.Code), strings.TrimSpace(rec.Body.String()))
	}
	return &prob
}
