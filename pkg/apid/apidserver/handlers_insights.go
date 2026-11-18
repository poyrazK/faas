package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/insights"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Product analytics (ADR 20261117084505 §3, §4), under
// /v1/projects/{slug}/environments/{environment}/insights. Every route is a
// bounded read.

// insightsDefaultDays is a query's range when the request sets none.
const insightsDefaultDays = 7

type insightsScope struct {
	acct    state.Account
	project state.Project
	env     state.ProjectEnvironment
	store   state.AnalyticsStore
}

func (s *server) insightsScope(w http.ResponseWriter, r *http.Request, acct state.Account) (insightsScope, bool) {
	if p, ok := principalFrom(r); ok && p.Key != nil && p.Key.AppID != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account key required", "product analytics require an account-scoped credential"))
		return insightsScope{}, false
	}
	if !s.platformFlagFor(platformflags.ProductAnalytics, flagSubject(r, acct)) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Product analytics unavailable", "product analytics are not enabled for this account").
			WithHint("An operator releases product analytics account by account. Your credentials are not the cause."))
		return insightsScope{}, false
	}
	store, ok := s.store.(state.AnalyticsStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("product analytics unavailable"))
		return insightsScope{}, false
	}
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return insightsScope{}, false
	}
	env, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, r.PathValue("environment"))
	if err != nil {
		writeFeatureFlagLookupError(w, err, fmt.Sprintf("environment %q not found in project %s", r.PathValue("environment"), project.Slug))
		return insightsScope{}, false
	}
	return insightsScope{acct: acct, project: project, env: env, store: store}, true
}

func writeInsightsError(w http.ResponseWriter, err error) {
	if errors.Is(err, insights.ErrInvalid) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid analytics query",
			strings.TrimPrefix(err.Error(), insights.ErrInvalid.Error()+": ")))
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "insights_query_timeout", "Analytics query timed out",
			"narrow the range, add property filters, or drop the breakdown"))
		return
	}
	api.WriteProblem(w, api.ErrInternal("product analytics unavailable"))
}

func queryTime(r *http.Request, name string) (*time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is an RFC 3339 time", insights.ErrInvalid, name)
	}
	return &t, nil
}

// queryWhere parses repeated where=KEY:VALUE parameters.
func queryWhere(r *http.Request) (map[string]string, error) {
	out := map[string]string{}
	for _, raw := range r.URL.Query()["where"] {
		k, v, ok := strings.Cut(raw, ":")
		if !ok {
			return nil, fmt.Errorf("%w: where is KEY:VALUE", insights.ErrInvalid)
		}
		out[k] = v
	}
	return out, nil
}

func (s *server) getInsightEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, ok := s.insightsScope(w, r, acct)
	if !ok {
		return
	}
	from, err := queryTime(r, "from")
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	until, err := queryTime(r, "until")
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	where, err := queryWhere(r)
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	rng, err := insights.ResolveRange(from, until, time.Now(), api.Plan(acct.Plan).AnalyticsRetentionDays(), insightsDefaultDays)
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AnalyticsQueryTimeout)
	defer cancel()
	q := r.URL.Query()
	out, err := insights.Series(ctx, sc.store, insights.SeriesQuery{EnvironmentID: sc.env.ID, Event: q.Get("event"), Where: where,
		Interval: q.Get("interval"), Unit: q.Get("unit"), By: q.Get("by"), Range: rng})
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) postInsightFunnel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, ok := s.insightsScope(w, r, acct)
	if !ok {
		return
	}
	var req api.InsightFunnelRequest
	if !decodeJSONLimit(w, r, &req, 32<<10) {
		return
	}
	rng, err := insights.ResolveRange(req.From, req.Until, time.Now(), api.Plan(acct.Plan).AnalyticsRetentionDays(), insightsDefaultDays)
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AnalyticsQueryTimeout)
	defer cancel()
	out, err := insights.Funnel(ctx, sc.store, insights.FunnelQuery{EnvironmentID: sc.env.ID, Request: req, Range: rng})
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) postInsightRetention(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, ok := s.insightsScope(w, r, acct)
	if !ok {
		return
	}
	var req api.InsightRetentionRequest
	if !decodeJSONLimit(w, r, &req, 32<<10) {
		return
	}
	now := time.Now()
	rng, err := insights.ResolveRange(req.From, req.Until, now, api.Plan(acct.Plan).AnalyticsRetentionDays(), 4*insightsDefaultDays)
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AnalyticsQueryTimeout)
	defer cancel()
	out, err := insights.Retention(ctx, sc.store, insights.RetentionQuery{EnvironmentID: sc.env.ID, Request: req, Range: rng, Now: now})
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getInsightRequests(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, ok := s.insightsScope(w, r, acct)
	if !ok {
		return
	}
	from, err := queryTime(r, "from")
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	until, err := queryTime(r, "until")
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	// Request telemetry keeps its own, shorter retention.
	rng, err := insights.ResolveRange(from, until, time.Now(), api.Plan(acct.Plan).DebugTelemetryRetentionDays(), 1)
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AnalyticsQueryTimeout)
	defer cancel()
	out, err := insights.Requests(ctx, sc.store, insights.RequestsQuery{AccountID: acct.ID, ProjectID: sc.project.ID,
		EnvironmentSlug: sc.env.Slug, Flag: r.URL.Query().Get("flag"), Range: rng})
	if err != nil {
		writeInsightsError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
