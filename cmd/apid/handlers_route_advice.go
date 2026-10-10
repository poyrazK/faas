package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeadvisor"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// routeAdviceReader is the PgStore surface the route advisor reads.
type routeAdviceReader interface {
	RouteAdviceRouteStats(context.Context, sqlc.RouteAdviceRouteStatsParams) ([]sqlc.RouteAdviceRouteStatsRow, error)
	RouteAdviceConsumers(context.Context, sqlc.RouteAdviceConsumersParams) ([]sqlc.RouteAdviceConsumersRow, error)
	RouteAdviceThrottleExcess(context.Context, sqlc.RouteAdviceThrottleExcessParams) (int64, error)
}

// getRouteAdvice serves GET /v1/apps/{slug}/routes/advice (ADR-955): edge-rule
// suggestions from retained request telemetry. It never writes; the customer
// applies a suggestion through the edge-rules API, disabled for review.
func (s *server) getRouteAdvice(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("analytics", acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	maxAge, err := routeAdviceCacheMaxAge(r.URL.Query().Get("cache_max_age"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if r.URL.Query().Get("since") == "" {
		q := r.URL.Query()
		q.Set("since", api.RouteAdviceDefaultSince.String())
		r.URL.RawQuery = q.Encode()
	}
	window, err := routeCustomerWindow(r, time.Now().UTC(), time.Duration(limits.DebugTelemetryRetentionDays)*24*time.Hour)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	reader, ok := s.store.(routeAdviceReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route advice"))
		return
	}
	in, err := s.routeAdviceInputs(r.Context(), reader, acct, app, window, maxAge)
	if err != nil {
		s.log.Warn("route advice read failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("route advice"))
		return
	}
	writeJSON(w, http.StatusOK, api.RouteAdviceResponse{
		Slug: app.Slug, From: window.From.Format(time.RFC3339Nano), Until: window.Until.Format(time.RFC3339Nano),
		WindowClamped: window.WindowClamped, CacheMaxAgeSeconds: maxAge,
		RoutesAnalyzed: len(in.Routes), Suggestions: routeadvisor.Advise(in),
	})
}

// routeAdviceCacheMaxAge parses the what-if cache lifetime; empty means the
// platform default for a new cache rule.
func routeAdviceCacheMaxAge(raw string) (int, error) {
	if raw == "" {
		return api.ResponseCacheDefaultMaxAgeSeconds, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > api.ResponseCacheMaxAgeMaxSeconds {
		return 0, fmt.Errorf("cache_max_age must be an integer between 1 and %d seconds", api.ResponseCacheMaxAgeMaxSeconds)
	}
	return n, nil
}

func (s *server) routeAdviceInputs(ctx context.Context, reader routeAdviceReader, acct state.Account, app state.App, window requestAnalyticsWindow, maxAge int) (routeadvisor.Inputs, error) {
	limits := api.MustLimitsFor(acct.Plan)
	in := routeadvisor.Inputs{
		CacheMaxAgeSeconds: maxAge,
		AsyncAllowed:       limits.AsyncInvokeAllowed && app.AcceptsRequestInvocations(),
		PlanMaxRPS:         limits.RateLimitRPS, PlanMaxBurst: limits.RateLimitBurst,
		ThrottleExcess: map[routeadvisor.Key]int64{},
	}
	appID, accountID := stringToPgUUID(app.ID), stringToPgUUID(acct.ID)
	since, until := pgtype.Timestamptz{Time: window.From, Valid: true}, pgtype.Timestamptz{Time: window.Until, Valid: true}
	stats, err := reader.RouteAdviceRouteStats(ctx, sqlc.RouteAdviceRouteStatsParams{
		AppID: appID, AccountID: accountID, SinceAt: since, UntilAt: until,
		RouteLimit: api.RouteAdviceMaxRoutes, CacheMaxAgeSeconds: int32(maxAge),
	})
	if err != nil {
		return in, fmt.Errorf("route advice stats: %w", err)
	}
	in.Routes = routeAdviceRoutes(stats)
	consumers, err := reader.RouteAdviceConsumers(ctx, sqlc.RouteAdviceConsumersParams{AppID: appID, AccountID: accountID, SinceAt: since, UntilAt: until})
	if err != nil {
		return in, fmt.Errorf("route advice consumers: %w", err)
	}
	in.Consumers = routeAdviceConsumers(consumers)
	if in.Existing, in.Hosts, err = s.routeAdviceRulesAndHosts(ctx, app); err != nil {
		return in, err
	}
	for key, limit := range routeadvisor.ThrottleCandidates(in) {
		excess, err := reader.RouteAdviceThrottleExcess(ctx, sqlc.RouteAdviceThrottleExcessParams{
			AllowancePerMinute: limit.AllowancePerMinute, AppID: appID, AccountID: accountID,
			ConsumerID: stringToPgUUID(limit.ConsumerID), Route: key.Method + " " + key.Path, Method: key.Method,
			SinceAt: since, UntilAt: until,
		})
		if err != nil {
			return in, fmt.Errorf("route advice throttle excess: %w", err)
		}
		in.ThrottleExcess[key] = excess
	}
	return in, nil
}

// routeAdviceRulesAndHosts returns the app's existing rules and the hosts a
// suggested rule attaches to: the platform hostname plus verified domains.
func (s *server) routeAdviceRulesAndHosts(ctx context.Context, app state.App) ([]routeadvisor.ExistingRule, []string, error) {
	rules, err := s.store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("route advice rules: %w", err)
	}
	existing := make([]routeadvisor.ExistingRule, 0, len(rules))
	for _, rule := range rules {
		existing = append(existing, routeadvisor.ExistingRule{Kind: string(rule.Kind), Path: rule.MatchPath, Methods: rule.MatchMethods})
	}
	domains, err := s.store.ListDomainsForApp(ctx, app.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("route advice domains: %w", err)
	}
	hosts := []string{appHostForDomain(app.Slug, s.domain)}
	for _, d := range domains {
		if !d.VerifiedAt.IsZero() && !slices.Contains(hosts, strings.ToLower(d.Domain)) {
			hosts = append(hosts, strings.ToLower(d.Domain))
		}
	}
	return existing, hosts, nil
}

func routeAdviceRoutes(rows []sqlc.RouteAdviceRouteStatsRow) []routeadvisor.RouteStats {
	out := make([]routeadvisor.RouteStats, 0, len(rows))
	for _, row := range rows {
		out = append(out, routeadvisor.RouteStats{
			Method: row.Method, Path: strings.TrimPrefix(row.Route, row.Method+" "),
			Requests: row.Requests, AnonymousRequests: row.AnonymousRequests, AnonymousSuccess: row.AnonymousSuccess,
			ServerErrors: row.ServerErrors, Timeouts: row.Timeouts, ColdBoots: row.ColdBoots,
			P95LatencyMs: int(row.P95LatencyMs), CacheHits: row.CacheHits, WakesAvoided: row.WakesAvoided,
		})
	}
	return out
}

func routeAdviceConsumers(rows []sqlc.RouteAdviceConsumersRow) []routeadvisor.ConsumerStats {
	var out []routeadvisor.ConsumerStats
	for _, row := range rows {
		path := strings.TrimPrefix(row.Route, row.Method+" ")
		peak := routeadvisor.ConsumerPeak{ID: uuidFromPg(row.ConsumerID), Requests: row.Requests, PeakPerMinute: row.PeakPerMinute}
		if n := len(out); n > 0 && out[n-1].Method == row.Method && out[n-1].Path == path {
			if row.ConsumerRank == 2 {
				out[n-1].Next = peak
			}
			continue
		}
		c := routeadvisor.ConsumerStats{Method: row.Method, Path: path, Consumers: row.Consumers}
		if row.ConsumerRank == 1 {
			c.Top = peak
		}
		out = append(out, c)
	}
	return out
}
