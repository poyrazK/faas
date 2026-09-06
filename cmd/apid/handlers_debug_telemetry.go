package main

// handlers_debug_telemetry.go — read-only slice of the production
// debugger (ADR-127) for PR-A. The write-side (publisher → gRPC
// IncrementRequestTelemetry → apid receiver → sqlc INSERT) lands in
// PR-B; PR-A ships the GET endpoint so customers can already see
// the existing app_errors_recorder-style rows once a row source is
// configured.
//
// Handlers: list recent requests and retrieve one request by id.
// The regression / compare / replay endpoints are PR-B / PR-C.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// debugTelemetryListHandler — GET /v1/apps/{slug}/debug/requests
//
// Plan-gated by DebugTelemetryEnabled. `since` is clamped to the
// plan's DebugTelemetryRetentionDays (Hobby 3d, Pro 7d, Scale 14d).
// `limit` defaults to 20, capped at 200 (matches
// handlers_invocations.go:451-455). The endpoint is IDOR-safe via
// loadApp (cross-account slug → 404).
func (s *server) debugTelemetryListHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	// since is a duration string ("3h", "24h", "7d"); we clamp to
	// the plan's retention cap so a Free user passing ?since=90d
	// is silently rounded down to DebugTelemetryRetentionDays.
	// The raw form is captured separately so the response can
	// echo it verbatim — round-trip safe (see echoDebugSince).
	sinceRaw := r.URL.Query().Get("since")
	sinceDur := parseDebugSinceFromString(sinceRaw, 24*time.Hour)
	cap := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	if cap > 0 && sinceDur > cap {
		sinceDur = cap
	}
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	now := time.Now().UTC()
	rows, err := s.store.ListRequestTelemetryByApp(r.Context(), sqlc.ListRequestTelemetryByAppParams{
		AppID:        stringToPgUUID(app.ID),
		ReceivedAt:   pgtype.Timestamptz{Time: now.Add(-sinceDur), Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now, Valid: true},
		Limit:        int32(limit),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list request telemetry"))
		return
	}
	if rows == nil {
		rows = []sqlc.ListRequestTelemetryByAppRow{}
	}
	items := make([]api.DebugTelemetryRequestItem, len(rows))
	for i, row := range rows {
		items[i] = debugTelemetryRowToItem(row)
	}
	writeJSON(w, http.StatusOK, api.DebugTelemetryListResponse{
		Requests: items,
		Since:    echoDebugSince(sinceRaw, sinceDur),
	})
}

// debugTelemetryGetHandler — GET /v1/apps/{slug}/debug/requests/{req_id}
//
// Direct lookup for a single request. Unlike the list endpoint, this
// does not depend on the request still being inside the first page of
// recent telemetry, which makes it usable for incident links and CLI
// drill-downs on busy apps. loadApp + app_id in the query keep it
// IDOR-safe.
func (s *server) debugTelemetryGetHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	reqID, err := uuid.Parse(r.PathValue("req_id"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("req_id must be a UUID"))
		return
	}
	now := time.Now().UTC()
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	row, err := s.store.GetRequestTelemetryByAppAndID(r.Context(), sqlc.GetRequestTelemetryByAppAndIDParams{
		AppID:        stringToPgUUID(app.ID),
		ID:           pgtype.UUID{Bytes: reqID, Valid: true},
		ReceivedAt:   pgtype.Timestamptz{Time: now.Add(-retention), Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "request telemetry not found"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("get request telemetry"))
		return
	}
	writeJSON(w, http.StatusOK, debugTelemetryGetRowToItem(row))
}

// debugTelemetryRowToItem maps a sqlc-generated row to the wire
// DTO. Lives in cmd/apid/ because pkg/api cannot import pkg/state
// (import cycle). The mapping handles pgtype.UUID → string
// (hyphenated hex), pgtype.Timestamptz → RFC3339Nano, and
// pgtype.Text (nullable trace_id) → *string.
func debugTelemetryRowToItem(row sqlc.ListRequestTelemetryByAppRow) api.DebugTelemetryRequestItem {
	return debugTelemetryItemFromFields(
		row.ID,
		row.DeploymentID,
		row.Route,
		row.Method,
		row.Status,
		row.LatencyMs,
		row.ColdBoot,
		row.TraceID,
		row.ReceivedAt,
	)
}

func debugTelemetryGetRowToItem(row sqlc.GetRequestTelemetryByAppAndIDRow) api.DebugTelemetryRequestItem {
	return debugTelemetryItemFromFields(
		row.ID,
		row.DeploymentID,
		row.Route,
		row.Method,
		row.Status,
		row.LatencyMs,
		row.ColdBoot,
		row.TraceID,
		row.ReceivedAt,
	)
}

func debugTelemetryItemFromFields(
	id, deploymentID pgtype.UUID,
	route, method string,
	status, latencyMS int32,
	coldBoot bool,
	traceID pgtype.Text,
	receivedAt pgtype.Timestamptz,
) api.DebugTelemetryRequestItem {
	item := api.DebugTelemetryRequestItem{
		// pgtype.UUID -> hyphenated hex string. Falls back to "" when
		// Valid=false so the JSON renders "" rather than the driver's
		// base64 zero-bytes shape.
		ID:           uuidFromPg(id),
		DeploymentID: uuidFromPg(deploymentID),
		Route:        route,
		Method:       method,
		Status:       int(status),
		LatencyMS:    int(latencyMS),
		ColdBoot:     coldBoot,
		ReceivedAt:   timeFromPg(receivedAt),
	}
	if traceID.Valid {
		s := traceID.String
		item.TraceID = &s
	}
	return item
}

// uuidFromPg renders a pgtype.UUID as the canonical hyphenated-hex
// string. Empty when !Valid so the wire shows "" rather than the
// driver's zero-bytes encoding.
func uuidFromPg(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// timeFromPg renders a pgtype.Timestamptz as an RFC3339Nano string
// (matches the format the rest of the apid surface uses for
// timestamps). Empty when !Valid.
func timeFromPg(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339Nano)
}

// echoDebugSince produces a wire-stable echo of a `since` request
// parameter so that customer automation can feed the response
// straight back into a follow-up request without re-parsing.
// When the customer supplied a parseable value AND the effective
// duration matches (no clamp), the original raw form is returned
// verbatim — round-trip safe (`?since=5d` → `"5d"`, not
// `120h0m0s` which parseDebugSinceFromString would accept but
// downstream tooling rarely normalizes to). When the effective
// duration was clamped by the plan cap, or when no raw value
// was supplied, the effective duration is rendered in the
// canonical `Nh` or `Nd` form so the customer can detect the
// discrepancy (or supply a default that round-trips).
func echoDebugSince(raw string, eff time.Duration) string {
	if raw != "" && parseDebugSinceFromString(raw, -1) == eff {
		return raw
	}
	if eff <= 0 {
		return ""
	}
	if eff%(24*time.Hour) == 0 {
		n := int(eff / (24 * time.Hour))
		return strconv.Itoa(n) + "d"
	}
	return eff.String()
}

// stringToPgUUID converts a hyphenated-hex UUID string into the
// pgtype.UUID shape the sqlc-generated queries expect. Invalid
// strings produce a zero-UUID value with Valid=false so the
// Postgres driver returns no rows rather than an error.
func stringToPgUUID(s string) pgtype.UUID {
	uid, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: uid, Valid: true}
}

// debugRegressionsHandler — GET /v1/apps/{slug}/debug/regressions
//
// Returns active regression observations for an app, ordered by
// regression_factor DESC then last_detected_at DESC (worst
// first). Plan-gated by DebugTelemetryEnabled. `since` is
// clamped to the plan's DebugTelemetryRetentionDays.
//
// The endpoint backs the dashboard regression banner
// (pkg/dashboard/templates/app_debug.html, PR-B) and the
// `gregale debug regressions <slug>` CLI verb.
func (s *server) debugRegressionsHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	sinceRaw := r.URL.Query().Get("since")
	sinceDur := parseDebugSinceFromString(sinceRaw, 1*time.Hour)
	cap := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	if cap > 0 && sinceDur > cap {
		sinceDur = cap
	}
	rows, err := s.store.ListActiveRegressionsByApp(r.Context(), sqlc.ListActiveRegressionsByAppParams{
		AppID: stringToPgUUID(app.ID),
		Column2: pgtype.Interval{
			Microseconds: int64(sinceDur / time.Microsecond),
			Valid:        true,
		},
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list regressions"))
		return
	}
	if rows == nil {
		rows = []sqlc.ListActiveRegressionsByAppRow{}
	}
	items := make([]api.DebugRegressionItem, len(rows))
	for i, row := range rows {
		items[i] = debugRegressionRowToItem(row)
	}
	writeJSON(w, http.StatusOK, api.DebugRegressionsResponse{
		Since:       echoDebugSince(sinceRaw, sinceDur),
		Regressions: items,
	})
}

// debugRegressionRowToItem maps a sqlc row to the wire DTO.
func debugRegressionRowToItem(row sqlc.ListActiveRegressionsByAppRow) api.DebugRegressionItem {
	item := api.DebugRegressionItem{
		DeploymentID:    uuidFromPg(row.DeploymentID),
		Route:           row.Route,
		P95MS:           int(row.P95Ms),
		P95BaseMS:       int(row.P95BaseMs),
		AffectedCount:   int(row.AffectedCount),
		FirstDetectedAt: timeFromPg(row.FirstDetectedAt),
		LastDetectedAt:  timeFromPg(row.LastDetectedAt),
	}
	// Numeric factor → string. pgtype.Numeric has its own
	// Float64Value helper; we render via pgx's numeric decoder
	// to avoid the precision drift of the marshaller.
	if row.RegressionFactor.Valid {
		f, err := row.RegressionFactor.Float64Value()
		if err == nil {
			item.Factor = formatFloat2(f.Float64)
		}
	}
	return item
}

// formatFloat2 renders a float with up to 2 decimal places —
// matches the schema's NUMERIC(5,2) precision. Used for the
// regression_factor wire field; "1.20", "2.43", "1.00".
func formatFloat2(v float64) string {
	if v <= 0 {
		return "0.00"
	}
	return fmt.Sprintf("%.2f", v)
}

// debugCompareHandler — POST /v1/apps/{slug}/debug/compare
//
// Compares two deployments' per-route latency distributions in a
// shared time window. Body shape: DebugCompareRequest (source,
// mirror deployment_ids + optional route filter + optional
// window bounds). Returns DebugCompareResponse with one row per
// route that shipped traffic in both deployments.
//
// PR-B composes two PR-A queries (RequestTelemetryBaselineP95ByRoute)
// in Go — the CTE-on-CTE shape trips sqlc v1.31's parser (the
// workaround is documented in queries.sql.go:5626-5631).
//
// Plan-gated by DebugTelemetryEnabled.
func (s *server) debugCompareHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	var req api.DebugCompareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid compare body"))
		return
	}
	if _, err := uuid.Parse(req.Source); err != nil {
		api.WriteProblem(w, api.ErrValidation("source must be a deployment id"))
		return
	}
	if _, err := uuid.Parse(req.Mirror); err != nil {
		api.WriteProblem(w, api.ErrValidation("mirror must be a deployment id"))
		return
	}
	// Default window: last 1h. Clamp to plan retention.
	sinceDur := parseDebugSinceFromString(req.Since, 1*time.Hour)
	capDur := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	if capDur > 0 && sinceDur > capDur {
		sinceDur = capDur
	}
	until := time.Now().UTC()
	if req.Until != "" {
		if t, err := time.Parse(time.RFC3339, req.Until); err == nil {
			until = t.UTC()
		}
	}
	from := until.Add(-sinceDur)
	srcID := stringToPgUUID(req.Source)
	mirID := stringToPgUUID(req.Mirror)

	// Fetch both distributions. Both calls share one index scan
	// over request_telemetry_app_dep_received_idx — four
	// aggregates (p50/p95/p99/COUNT) in a single pass (Debugger
	// UX v1 stage 3 sqlc rewrite; PR-B minimum was p95-only and
	// walked client-side for the others).
	srcStats, err := s.fetchRouteStats(r.Context(), app.ID, srcID, from, until, req.Route)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("compare source"))
		return
	}
	mirStats, err := s.fetchRouteStats(r.Context(), app.ID, mirID, from, until, req.Route)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("compare mirror"))
		return
	}
	// Merge: union of routes; missing entries render zero stats.
	// A missing side means zero rows in the window — DO NOT
	// synthesize percentiles from the other side. Test (b) in
	// handlers_debug_telemetry_compare_test.go asserts the
	// N=0/percentiles=0 contract.
	routes := make(map[string]api.DebugCompareRouteStats, len(srcStats)+len(mirStats))
	for route, s := range srcStats {
		routes[route] = api.DebugCompareRouteStats{
			Route:     route,
			SourceP50: s.P50, SourceP95: s.P95, SourceP99: s.P99, SourceN: s.N,
		}
	}
	for route, m := range mirStats {
		existing := routes[route]
		existing.Route = route
		existing.MirrorP50 = m.P50
		existing.MirrorP95 = m.P95
		existing.MirrorP99 = m.P99
		existing.MirrorN = m.N
		routes[route] = existing
	}
	// Stable ordering for deterministic dashboard rendering.
	out := make([]api.DebugCompareRouteStats, 0, len(routes))
	for _, v := range routes {
		out = append(out, v)
	}
	sortRouteStats(out)
	writeJSON(w, http.StatusOK, api.DebugCompareResponse{
		Source: req.Source,
		Mirror: req.Mirror,
		Routes: out,
	})
}

// routeStats is the per-route aggregate used by the compare
// endpoint. Debugger UX v1 stage 3 extended
// RequestTelemetryBaselineP95ByRoute to return p50/p95/p99/N
// from a single index scan; this struct mirrors that shape so
// the dashboard can render the full latency distribution
// alongside the request count.
type routeStats struct {
	P50 int
	P95 int
	P99 int
	N   int64
}

// fetchRouteStats reads the per-route p50/p95/p99 + row count
// for a single deployment in the window. The shape mirrors the
// regression cron (cmd/apid/debug_regression_cron.go) — same
// percentile_cont aggregate, same window split, single index
// scan over request_telemetry_app_dep_received_idx (PR-A
// migration 00427).
//
// Returns an empty map (not nil) when no rows match so the
// caller doesn't have to nil-check. A deployment that had zero
// traffic in the window maps to absent entries (not zero-valued
// entries) — the caller synthesizes the zero-valued DTO so the
// merge loop stays symmetric.
func (s *server) fetchRouteStats(ctx context.Context, appID string, deploymentID pgtype.UUID, from, until time.Time, route string) (map[string]routeStats, error) {
	rows, err := s.store.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{
		AppID:        stringToPgUUID(appID),
		DeploymentID: deploymentID,
		ReceivedAt:   pgtype.Timestamptz{Time: from, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: until, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]routeStats, len(rows))
	for _, row := range rows {
		if route != "" && row.Route != route {
			continue
		}
		out[row.Route] = routeStats{
			P50: int(row.P50Ms),
			P95: int(row.P95Ms),
			P99: int(row.P99Ms),
			N:   row.N,
		}
	}
	return out, nil
}

// sortRouteStats sorts by route name (stable, deterministic
// output for dashboard diffing).
func sortRouteStats(stats []api.DebugCompareRouteStats) {
	// insertion sort — the slice is typically <100 routes
	for i := 1; i < len(stats); i++ {
		j := i
		for j > 0 && stats[j-1].Route > stats[j].Route {
			stats[j-1], stats[j] = stats[j], stats[j-1]
			j--
		}
	}
}

// parseDebugSinceFromString is a variant of parseDebugSince that
// takes the raw string directly (the compare body has its own
// since field, not a query param). Empty / unparseable → def.
func parseDebugSinceFromString(raw string, def time.Duration) time.Duration {
	if raw == "" {
		return def
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(raw[:len(raw)-1]); err == nil && raw[len(raw)-1] == 'd' && n > 0 {
		return time.Duration(n) * 24 * time.Hour
	}
	return def
}

// debugReplayHandler — POST /v1/apps/{slug}/debug/requests/{req_id}/replay
//
// PR-B stub. The full replay path lands with issue #72 PR-A2
// (traffic mirror PR-A2, in worktree feat-issue-72-traffic-mirror-pr-a2):
// the mirror invocation handler accepts an upstream request id
// and routes the recorded headers/body to the customer's
// mirror deployment.
//
// PR-B returns the mirror_invocation_id that PR-A2 will create
// (when implemented) — the response shape is stable across the
// two PRs so the customer's automation can wire once.
//
// Plan-gated by DebugTelemetryEnabled.
func (s *server) debugReplayHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	reqID := r.PathValue("req_id")
	if _, err := uuid.Parse(reqID); err != nil {
		api.WriteProblem(w, api.ErrValidation("req_id must be a UUID"))
		return
	}
	// PR-B does not yet invoke the mirror pipeline (issue #72
	// PR-A2 owns the scheduler-side mirror invocation). We return
	// a stable shape so the customer's tooling can wire against
	// it; the status field signals "queued" so the dashboard
	// renders a "Replay queued — PR-A2 will route it" tile.
	writeJSON(w, http.StatusAccepted, api.DebugReplayResponse{
		Status: "queued",
	})
	_ = app // app loaded for plan-gating; not used in the stub body
}
