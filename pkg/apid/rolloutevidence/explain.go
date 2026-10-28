// Package rolloutevidence explains canary rollouts with request evidence and
// builds the app debug status (ADR-127 amendment following ADR-1447).
//
// ADR-1447 records why the safe-release circuit breaker advanced, held, or
// aborted a rollout with aggregates only. This package reads the request
// telemetry behind the same deployment and window at request time: which
// routes did worse than the stable deployment, example requests that show the
// problem, and the candidate's open regressions. Nothing is stored, so the
// decision record stays aggregate-only.
package rolloutevidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrDeploymentNotFound is returned when an explicit deployment ID does not
// belong to the app.
var ErrDeploymentNotFound = errors.New("rolloutevidence: deployment not found")

// exampleScan bounds the candidate rows read to find failing examples.
const exampleScan = 100

// Store is the state surface the reader needs; state.Store satisfies it.
type Store interface {
	DeploymentByID(ctx context.Context, id string) (state.Deployment, error)
	ListDeploymentsForApp(ctx context.Context, appID string, limit, offset int) ([]state.Deployment, error)
	LiveDeployments(ctx context.Context, appID string) ([]state.Deployment, error)
	RequestTelemetryErrorsByRoute(ctx context.Context, arg sqlc.RequestTelemetryErrorsByRouteParams) ([]sqlc.RequestTelemetryErrorsByRouteRow, error)
	RequestTelemetryBaselineP95ByRoute(ctx context.Context, arg sqlc.RequestTelemetryBaselineP95ByRouteParams) ([]sqlc.RequestTelemetryBaselineP95ByRouteRow, error)
	RequestTelemetryAnalyticsSummary(ctx context.Context, arg sqlc.RequestTelemetryAnalyticsSummaryParams) (sqlc.RequestTelemetryAnalyticsSummaryRow, error)
	ListRequestTelemetryByApp(ctx context.Context, arg sqlc.ListRequestTelemetryByAppParams) ([]sqlc.ListRequestTelemetryByAppRow, error)
}

// HoldReader reads the newest rollout hold (ADR-1447);
// state.RolloutDecisionStore satisfies it.
type HoldReader interface {
	LatestRolloutHold(ctx context.Context, deploymentID string) (json.RawMessage, error)
}

// Reader explains rollouts. Holds may be nil; Regressions and RequestItem are
// supplied by apidserver so the response shapes match the other debugger
// endpoints.
type Reader struct {
	Store Store
	Holds HoldReader
	// Regressions lists the app's open regressions seen within since.
	Regressions func(ctx context.Context, appID string, since time.Duration) ([]api.DebugRegressionItem, error)
	// OOMKills lists the app's out-of-memory kills in [start, end], newest
	// first, at most api.DebugStatusMaxOOMKills, and their total.
	OOMKills func(ctx context.Context, appID string, start, end time.Time) ([]api.DebugStatusOOMKill, int, error)
	// RequestItem converts a telemetry row for the response.
	RequestItem func(sqlc.ListRequestTelemetryByAppRow) api.DebugTelemetryRequestItem
	Now         func() time.Time
}

func (r Reader) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Explain explains deploymentID's rollout, or the newest of the app's recent
// deployments that had a canary rollout when deploymentID is empty. The
// evidence window is clamped to retention.
func (r Reader) Explain(ctx context.Context, app state.App, deploymentID string, retention time.Duration) (api.DebugRolloutExplanation, error) {
	now := r.now()
	out := api.DebugRolloutExplanation{
		State: api.DebugRolloutNone, Routes: []api.DebugRolloutRoute{}, Examples: []api.DebugTelemetryRequestItem{},
		Regressions: []api.DebugRegressionItem{}, GeneratedAt: now.Format(time.RFC3339Nano),
	}
	dep, found, err := r.rolloutDeployment(ctx, app.ID, deploymentID)
	if err != nil {
		return out, err
	}
	if !found {
		out.Headline = "No canary rollout among the app's recent deployments."
		return out, nil
	}
	out.DeploymentID, out.TrafficPercent = dep.ID, dep.TrafficPercent
	out.State, out.Decision = r.decision(ctx, dep)
	if out.State == api.DebugRolloutAborted && out.Decision == nil {
		out.AbortedReason = dep.RolloutAbortedReason
	}
	start, end := window(dep, out.State, out.Decision, now)
	if retention > 0 && start.Before(now.Add(-retention)) {
		start, out.RetentionClamped = now.Add(-retention), true
	}
	if !end.After(start) {
		out.Headline = headline(out)
		return out, nil
	}
	out.WindowStart, out.WindowEnd = start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)
	if out.Decision != nil {
		out.StableDeploymentID = out.Decision.StableDeploymentID
	}
	if out.StableDeploymentID == "" {
		out.StableDeploymentID = r.stableDeployment(ctx, app.ID, dep.ID)
	}
	routes, err := r.compareRoutes(ctx, app.ID, dep.ID, out.StableDeploymentID, start, end)
	if err != nil {
		return out, err
	}
	rankRoutes(routes, rule(out.Decision))
	if len(routes) > api.DebugRolloutMaxRoutes {
		routes = routes[:api.DebugRolloutMaxRoutes]
	}
	out.Routes = routes
	if len(routes) > 0 {
		if out.Examples, err = r.examples(ctx, app.ID, dep.ID, routes[0], rule(out.Decision), start, end); err != nil {
			return out, err
		}
	}
	if r.Regressions != nil {
		items, err := r.Regressions(ctx, app.ID, max(now.Sub(start), time.Hour))
		if err != nil {
			return out, err
		}
		for _, item := range items {
			if item.DeploymentID == dep.ID {
				out.Regressions = append(out.Regressions, item)
			}
		}
	}
	out.Headline = headline(out)
	return out, nil
}

// rolloutDeployment resolves the deployment to explain. found is false when
// no recent deployment had a canary rollout.
func (r Reader) rolloutDeployment(ctx context.Context, appID, deploymentID string) (state.Deployment, bool, error) {
	if deploymentID != "" {
		dep, err := r.Store.DeploymentByID(ctx, deploymentID)
		if errors.Is(err, state.ErrNotFound) || err == nil && dep.AppID != appID {
			return state.Deployment{}, false, ErrDeploymentNotFound
		}
		if err != nil {
			return state.Deployment{}, false, fmt.Errorf("rolloutevidence: read deployment: %w", err)
		}
		return dep, true, nil
	}
	deps, err := r.Store.ListDeploymentsForApp(ctx, appID, api.DebugRolloutSearchDeployments, 0)
	if err != nil {
		return state.Deployment{}, false, fmt.Errorf("rolloutevidence: list deployments: %w", err)
	}
	for _, dep := range deps {
		if dep.CanaryTotalSteps > 0 || dep.RolloutState == api.DebugRolloutAborted || len(dep.RolloutDecision) > 0 {
			return dep, true, nil
		}
	}
	return state.Deployment{}, false, nil
}

// decision maps the deployment's rollout onto the customer-facing state and
// the ADR-1447 record that explains it. A record is used only when its kind
// explains the state: abort for an abort, advance for progress or completion.
func (r Reader) decision(ctx context.Context, dep state.Deployment) (string, *api.RolloutDecision) {
	recorded := release.DecodeDecision(dep.RolloutDecision)
	ofKind := func(kind string) *api.RolloutDecision {
		if recorded != nil && recorded.Kind == kind {
			return recorded
		}
		return nil
	}
	switch strings.TrimSpace(dep.RolloutState) {
	case api.DebugRolloutAborted:
		return api.DebugRolloutAborted, ofKind(api.RolloutDecisionAbort)
	case api.DebugRolloutComplete:
		return api.DebugRolloutComplete, ofKind(api.RolloutDecisionAdvance)
	}
	if dep.CanaryTotalSteps <= 0 {
		return api.DebugRolloutNone, nil
	}
	if r.Holds != nil {
		// A hold is an explanation, not state: a read failure falls back to
		// the latest advance.
		if raw, err := r.Holds.LatestRolloutHold(ctx, dep.ID); err == nil {
			if hold := release.DecodeDecision(raw); hold != nil {
				return api.DebugRolloutHeld, hold
			}
		}
	}
	return api.DebugRolloutRollingOut, ofKind(api.RolloutDecisionAdvance)
}

// window is the interval the evidence covers: the decision's observation
// window when it has one, otherwise the current step (or whole rollout) up to
// its end. A hold is explained up to now, since the canary is still waiting.
func window(dep state.Deployment, rolloutState string, decision *api.RolloutDecision, now time.Time) (time.Time, time.Time) {
	start := firstTime(dep.CanaryStepStartedAt, dep.RolloutStartedAt, &dep.CreatedAt)
	end := now
	switch rolloutState {
	case api.DebugRolloutAborted:
		end = derefOr(dep.RolloutAbortedAt, now)
	case api.DebugRolloutComplete:
		start = firstTime(dep.RolloutStartedAt, &dep.CreatedAt)
		end = derefOr(dep.RolloutCompletedAt, now)
	}
	if decision != nil {
		if t, err := time.Parse(time.RFC3339Nano, decision.WindowStart); err == nil {
			start = t
		}
		if t, err := time.Parse(time.RFC3339Nano, decision.WindowEnd); err == nil && decision.Kind != api.RolloutDecisionHold {
			end = t
		}
	}
	if end.After(now) {
		end = now
	}
	return start.UTC(), end.UTC()
}

// stableDeployment is the other serving deployment with the most traffic,
// used when no decision names the stable one.
func (r Reader) stableDeployment(ctx context.Context, appID, candidateID string) string {
	live, err := r.Store.LiveDeployments(ctx, appID)
	if err != nil {
		return ""
	}
	best := ""
	bestTraffic := -1
	for _, dep := range live {
		if dep.ID != candidateID && dep.TrafficPercent > bestTraffic {
			best, bestTraffic = dep.ID, dep.TrafficPercent
		}
	}
	return best
}

// compareRoutes joins the candidate's per-route errors and p95 with the
// stable deployment's over the same window. Routes the candidate did not
// serve are omitted.
func (r Reader) compareRoutes(ctx context.Context, appID, candidateID, stableID string, start, end time.Time) ([]api.DebugRolloutRoute, error) {
	byRoute := map[string]*api.DebugRolloutRoute{}
	errs, err := r.Store.RequestTelemetryErrorsByRoute(ctx, sqlc.RequestTelemetryErrorsByRouteParams{
		AppID: pgUUID(appID), DeploymentID: pgUUID(candidateID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(end),
	})
	if err != nil {
		return nil, fmt.Errorf("rolloutevidence: candidate errors: %w", err)
	}
	for _, row := range errs {
		byRoute[row.Route] = &api.DebugRolloutRoute{Route: row.Route, CandidateRequests: row.Requests, CandidateServerErrors: row.ErrorRequests}
	}
	if len(byRoute) == 0 {
		return []api.DebugRolloutRoute{}, nil
	}
	p95, err := r.Store.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{
		AppID: pgUUID(appID), DeploymentID: pgUUID(candidateID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(end),
	})
	if err != nil {
		return nil, fmt.Errorf("rolloutevidence: candidate latency: %w", err)
	}
	for _, row := range p95 {
		if route := byRoute[row.Route]; route != nil {
			route.CandidateP95MS = int(row.P95Ms)
		}
	}
	if stableID != "" {
		if err := r.addStable(ctx, appID, stableID, start, end, byRoute); err != nil {
			return nil, err
		}
	}
	routes := make([]api.DebugRolloutRoute, 0, len(byRoute))
	for _, route := range byRoute {
		routes = append(routes, *route)
	}
	return routes, nil
}

func (r Reader) addStable(ctx context.Context, appID, stableID string, start, end time.Time, byRoute map[string]*api.DebugRolloutRoute) error {
	errs, err := r.Store.RequestTelemetryErrorsByRoute(ctx, sqlc.RequestTelemetryErrorsByRouteParams{
		AppID: pgUUID(appID), DeploymentID: pgUUID(stableID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(end),
	})
	if err != nil {
		return fmt.Errorf("rolloutevidence: stable errors: %w", err)
	}
	for _, row := range errs {
		if route := byRoute[row.Route]; route != nil {
			route.StableRequests, route.StableServerErrors = row.Requests, row.ErrorRequests
		}
	}
	p95, err := r.Store.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{
		AppID: pgUUID(appID), DeploymentID: pgUUID(stableID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(end),
	})
	if err != nil {
		return fmt.Errorf("rolloutevidence: stable latency: %w", err)
	}
	for _, row := range p95 {
		if route := byRoute[row.Route]; route != nil {
			route.StableP95MS = int(row.P95Ms)
		}
	}
	return nil
}

func rule(decision *api.RolloutDecision) string {
	if decision == nil {
		return ""
	}
	return decision.Rule
}

// latencyRule reports whether a rule is about latency, so the worst route is
// the slowest one rather than the most failing one.
func latencyRule(rule string) bool {
	switch rule {
	case api.RolloutRuleAbortP95Latency, api.RolloutRuleAbortPostReleaseP95, api.RolloutRuleAbortColdBootP95:
		return true
	}
	return false
}

// rankRoutes orders routes worst first: by p95 increase over stable for a
// latency rule, otherwise by server-error rate increase, then by errors and
// traffic. Route name breaks ties so the order is deterministic.
func rankRoutes(routes []api.DebugRolloutRoute, rule string) {
	errorExcess := func(route api.DebugRolloutRoute) float64 {
		return rate(route.CandidateServerErrors, route.CandidateRequests) - rate(route.StableServerErrors, route.StableRequests)
	}
	sort.SliceStable(routes, func(i, j int) bool {
		a, b := routes[i], routes[j]
		if latencyRule(rule) {
			if da, db := a.CandidateP95MS-a.StableP95MS, b.CandidateP95MS-b.StableP95MS; da != db {
				return da > db
			}
		}
		if ea, eb := errorExcess(a), errorExcess(b); ea != eb {
			return ea > eb
		}
		if a.CandidateServerErrors != b.CandidateServerErrors {
			return a.CandidateServerErrors > b.CandidateServerErrors
		}
		if a.CandidateRequests != b.CandidateRequests {
			return a.CandidateRequests > b.CandidateRequests
		}
		return a.Route < b.Route
	})
}

func rate(failed, requests int64) float64 {
	if requests <= 0 {
		return 0
	}
	return float64(failed) / float64(requests)
}

// examples picks candidate requests on the worst route that show the
// problem: the newest at or above the route's p95 for a latency rule,
// otherwise the newest server errors. None when the route shows neither.
func (r Reader) examples(ctx context.Context, appID, candidateID string, route api.DebugRolloutRoute, rule string, start, end time.Time) ([]api.DebugTelemetryRequestItem, error) {
	latency := latencyRule(rule) && route.CandidateP95MS > 0
	if !latency && route.CandidateServerErrors == 0 {
		return []api.DebugTelemetryRequestItem{}, nil
	}
	params := sqlc.ListRequestTelemetryByAppParams{
		AppID: pgUUID(appID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(end),
		Route: route.Route, DeploymentID: candidateID, ColdBootFilter: -1, Limit: exampleScan,
	}
	if latency {
		params.MinLatencyMs, params.Limit = int32(route.CandidateP95MS), api.DebugRolloutMaxExamples
	}
	rows, err := r.Store.ListRequestTelemetryByApp(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("rolloutevidence: example requests: %w", err)
	}
	out := []api.DebugTelemetryRequestItem{}
	for _, row := range rows {
		if len(out) == api.DebugRolloutMaxExamples {
			break
		}
		if latency || row.Status >= 500 {
			out = append(out, r.item(row))
		}
	}
	return out, nil
}

func (r Reader) item(row sqlc.ListRequestTelemetryByAppRow) api.DebugTelemetryRequestItem {
	if r.RequestItem != nil {
		return r.RequestItem(row)
	}
	return api.DebugTelemetryRequestItem{Route: row.Route, Method: row.Method, Status: int(row.Status), LatencyMS: int(row.LatencyMs)}
}

// headline explains the rollout in one line, then names the worst route when
// it shows the problem.
func headline(out api.DebugRolloutExplanation) string {
	var b strings.Builder
	reason := ""
	if out.Decision != nil {
		reason = out.Decision.Summary
		if reason == "" {
			reason = out.Decision.Rule
		}
	}
	switch out.State {
	case api.DebugRolloutAborted:
		// An abort returns the candidate's traffic to the stable release, so
		// the share it failed at is the decision's.
		if out.Decision != nil {
			fmt.Fprintf(&b, "Rolled back at %d%% traffic", out.Decision.TrafficPercent)
		} else {
			reason = out.AbortedReason
			b.WriteString("Rolled back")
		}
	case api.DebugRolloutHeld:
		fmt.Fprintf(&b, "Held at %d%% traffic", out.TrafficPercent)
	case api.DebugRolloutRollingOut:
		fmt.Fprintf(&b, "Rolling out at %d%% traffic", out.TrafficPercent)
		reason = ""
	case api.DebugRolloutComplete:
		b.WriteString("Rolled out to all traffic")
		reason = ""
	default:
		return "No canary rollout for this deployment."
	}
	if reason != "" {
		b.WriteString(": " + reason)
	}
	b.WriteString(".")
	if len(out.Routes) > 0 {
		route := out.Routes[0]
		switch {
		case latencyRule(rule(out.Decision)) && route.CandidateP95MS > route.StableP95MS:
			fmt.Fprintf(&b, " Slowest route %s: p95 %d ms vs %d ms on the stable release.", route.Route, route.CandidateP95MS, route.StableP95MS)
		case route.CandidateServerErrors > 0:
			fmt.Fprintf(&b, " Worst route %s: %d of %d requests failed with a server error, vs %d of %d on the stable release.",
				route.Route, route.CandidateServerErrors, route.CandidateRequests, route.StableServerErrors, route.StableRequests)
		}
	}
	return b.String()
}

func firstTime(times ...*time.Time) time.Time {
	for _, t := range times {
		if t != nil && !t.IsZero() {
			return *t
		}
	}
	return time.Time{}
}

func derefOr(t *time.Time, fallback time.Time) time.Time {
	if t == nil || t.IsZero() {
		return fallback
	}
	return *t
}

func pgUUID(id string) pgtype.UUID {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

func pgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
