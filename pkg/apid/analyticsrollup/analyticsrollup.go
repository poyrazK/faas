// Package analyticsrollup answers request analytics from the per-minute
// rollups (ADR-20261103113952). It turns rollup groups into the row shapes
// of the raw request_telemetry analytics queries, so the apid handlers build
// one response whichever store answered.
package analyticsrollup

import (
	"context"
	"log/slog"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OtherDimension is the folded group's value.
const OtherDimension = "__other__"

// Reader is the store the rollup reads use.
type Reader = state.RequestAnalyticsRollupReader

// Covers reports whether the app's rollups hold every minute from from on:
// rollups started at least RequestAnalyticsSettleLag before from, so the
// first, partial minute and an upgrade's stragglers are outside the window.
// Minutes are kept only RequestAnalyticsMinuteRetention (amendment 3), so a
// window starting past RequestAnalyticsMinuteReadHorizon is covered only
// when it starts on a whole hour the hourly tier holds; the apid window
// parser aligns such windows, ends included. A tier's coverage is backdated
// over a span with no raw telemetry (effectiveSince). Any error, or a store
// without rollups, means no.
func Covers(ctx context.Context, store any, appID string, from time.Time) (Reader, bool) {
	reader, ok := coversMinutes(ctx, store, appID, from)
	if !ok || !from.Before(time.Now().Add(-api.RequestAnalyticsMinuteReadHorizon)) {
		return reader, ok
	}
	id, _ := uuid.Parse(appID)
	hoursSince, has, err := reader.RequestAnalyticsRollupHoursCoverage(ctx, id)
	if err != nil || !has || !from.Equal(from.UTC().Truncate(time.Hour)) {
		return reader, false
	}
	if hoursSince, err = effectiveSince(ctx, reader, id, from, hoursSince); err != nil {
		return reader, false
	}
	return reader, !from.Before(ceilHour(hoursSince.Add(api.RequestAnalyticsSettleLag)))
}

func coversMinutes(ctx context.Context, store any, appID string, from time.Time) (Reader, bool) {
	reader, ok := store.(Reader)
	if !ok {
		return nil, false
	}
	id, err := uuid.Parse(appID)
	if err != nil {
		return nil, false
	}
	since, ok, err := reader.RequestAnalyticsRollupCoverage(ctx, id)
	if err != nil || !ok {
		return nil, false
	}
	if since, err = effectiveSince(ctx, reader, id, from, since); err != nil {
		return nil, false
	}
	return reader, !from.Before(since.Add(api.RequestAnalyticsSettleLag))
}

// effectiveSince backdates a tier's coverage to before from when the app
// has no raw telemetry received in [from, since): it had no traffic there
// that the tier misses, so the tier answers that span too (amendment 4).
// Without it a new app, or one idle when rollups started, read slow and
// ingest-capped raw rows for every window longer than its rollups' age.
func effectiveSince(ctx context.Context, reader Reader, id uuid.UUID, from, since time.Time) (time.Time, error) {
	if !from.Before(since) {
		return since, nil
	}
	has, err := reader.RequestTelemetryExistsBetween(ctx, id, from, since)
	if err != nil || has {
		return since, err
	}
	return from.Add(-api.RequestAnalyticsSettleLag), nil
}

// WindowIncompleteGap is the analytics telemetry gap reason for requests
// lost during the window; lost rollups report it too.
const WindowIncompleteGap = "telemetry_window_incomplete"

// WithRollupGaps adds lost rollups to a completeness answer: when the
// rollups answer the window and apid lost some of its minutes, the window
// is incomplete. An unknown answer (nil) stays unknown.
func WithRollupGaps(ctx context.Context, store any, log *slog.Logger, appID string, from, until time.Time, complete *bool, gaps []string) (*bool, []string) {
	if complete == nil {
		return complete, gaps
	}
	reader, ok := Covers(ctx, store, appID, from)
	id, parseErr := uuid.Parse(appID)
	if !ok || parseErr != nil {
		return complete, gaps
	}
	has, err := reader.RequestAnalyticsRollupHasGap(ctx, id, from, until)
	if err != nil {
		if log != nil {
			log.Warn("request analytics rollup gap check failed", "app_id", appID, "err", err)
		}
		return nil, nil
	}
	if !has {
		return complete, gaps
	}
	incomplete := false
	if !slices.Contains(gaps, WindowIncompleteGap) {
		gaps = append(gaps, WindowIncompleteGap)
	}
	return &incomplete, gaps
}

// HourRange is the part of [from, until) read from the hourly tier: the
// whole UTC hours inside the window that start at or after the hourly
// tier's coverage plus RequestAnalyticsSettleLag (amendment 2). The minute
// tier answers the rest. An empty range is returned as [until, until).
func HourRange(from, until, hoursSince time.Time, hasHours bool) (time.Time, time.Time) {
	if !hasHours {
		return until, until
	}
	start := from
	if settled := hoursSince.Add(api.RequestAnalyticsSettleLag); settled.After(start) {
		start = settled
	}
	hoursFrom := ceilHour(start)
	hoursUntil := until.UTC().Truncate(time.Hour)
	if !hoursFrom.Before(hoursUntil) {
		return until, until
	}
	return hoursFrom, hoursUntil
}

// AlignWindow widens the ends of an analytics window that lie past
// RequestAnalyticsMinuteReadHorizon to whole UTC hours: minute rollups are
// kept only RequestAnalyticsMinuteRetention, so the hourly tier answers that
// part (amendment 3). from moves back to its hour, or forward when that hour
// starts before the plan's retention, whose hour rows are swept; until moves
// forward to its next hour. Ends inside the horizon are kept, so a recent
// window still ends at now. A zero retention means unlimited.
func AlignWindow(from, until, now time.Time, retention time.Duration) (time.Time, time.Time) {
	from, until, now = from.UTC(), until.UTC(), now.UTC()
	horizon := now.Add(-api.RequestAnalyticsMinuteReadHorizon)
	if from.Before(horizon) {
		floor := from.Truncate(time.Hour)
		if retention > 0 && floor.Before(now.Add(-retention)) {
			floor = ceilHour(from)
		}
		from = floor
	}
	if until.Before(horizon) {
		until = ceilHour(until)
	}
	if !from.Before(until) {
		// A window inside one hour at the retention edge keeps that hour.
		until = from.Add(time.Hour)
	}
	return from, until
}

func ceilHour(t time.Time) time.Time {
	floor := t.UTC().Truncate(time.Hour)
	if floor.Equal(t) {
		return floor
	}
	return floor.Add(time.Hour)
}

// hourRange looks up the app's hourly coverage and returns its HourRange.
func hourRange(ctx context.Context, reader Reader, appID string, from, until time.Time) (pgtype.Timestamptz, pgtype.Timestamptz, error) {
	since, ok := time.Time{}, false
	if id, err := uuid.Parse(appID); err == nil {
		if since, ok, err = reader.RequestAnalyticsRollupHoursCoverage(ctx, id); err != nil {
			return pgtype.Timestamptz{}, pgtype.Timestamptz{}, err
		}
		if ok {
			if since, err = effectiveSince(ctx, reader, id, from, since); err != nil {
				return pgtype.Timestamptz{}, pgtype.Timestamptz{}, err
			}
		}
	}
	hf, hu := HourRange(from, until, since, ok)
	return pgtype.Timestamptz{Time: hf, Valid: true}, pgtype.Timestamptz{Time: hu, Valid: true}, nil
}

// RouteDeployments returns requests per route, method and deployment from
// the rollups.
func RouteDeployments(ctx context.Context, reader Reader, appID, accountID string, from, until time.Time) (map[RouteDeploymentKey]int64, error) {
	hoursFrom, hoursUntil, err := hourRange(ctx, reader, appID, from, until)
	if err != nil {
		return nil, err
	}
	rows, err := reader.RequestAnalyticsRollupRouteDeployments(ctx, sqlc.RequestAnalyticsRollupRouteDeploymentsParams{
		AppID: pgUUID(appID), AccountID: pgUUID(accountID),
		WindowFrom:  pgtype.Timestamptz{Time: from, Valid: true},
		WindowUntil: pgtype.Timestamptz{Time: until, Valid: true},
		HoursFrom:   hoursFrom,
		HoursUntil:  hoursUntil,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[RouteDeploymentKey]int64, len(rows))
	for _, row := range rows {
		out[RouteDeploymentKey{Route: row.Route, Method: row.Method, DeploymentID: uuid.UUID(row.DeploymentID.Bytes).String()}] += row.Requests
	}
	return out, nil
}

// RouteDeploymentKey is one route, method and deployment.
type RouteDeploymentKey struct{ Route, Method, DeploymentID string }

// Query is one rollup read.
type Query struct {
	AppID, AccountID string
	From, Until      time.Time
	// GroupBy is an analytics dimension: route, status, country,
	// referrer_host, ua_family or consumer_id.
	GroupBy string
	// Route and Method filter route rows; GroupBy must be route or status.
	Route, Method string
	Limit         int
	Hourly        bool
}

// SupportsFilter reports whether the rollups can answer groupBy with the
// route/method filter: only route rows carry a route.
func SupportsFilter(groupBy, route, method string) bool {
	if route == "" && method == "" {
		return true
	}
	return groupBy == "" || groupBy == "route" || groupBy == "status"
}

// Group is one merged rollup group.
type Group struct {
	Bucket        time.Time
	Dimension     string
	Method        string
	Requests      int64
	ErrorRequests int64
	ColdBoots     int64
	RequestTimeMS int64
	LatencyMaxMS  int32
	LatencyHist   []int64
}

// P returns the group's q-quantile latency.
func (g Group) P(q float64) int32 {
	return int32(state.RequestAnalyticsHistPercentile(g.LatencyHist, g.LatencyMaxMS, q))
}

// Read returns the query's groups, with rows that share a bucket, dimension
// and method merged (a value the writer folded into __other__ meets the
// reader's own __other__).
func Read(ctx context.Context, reader Reader, q Query) ([]Group, error) {
	groupBy := q.GroupBy
	if groupBy == "" {
		groupBy = "route"
	}
	hoursFrom, hoursUntil, err := hourRange(ctx, reader, q.AppID, q.From, q.Until)
	if err != nil {
		return nil, err
	}
	rows, err := reader.RequestAnalyticsRollupGroups(ctx, sqlc.RequestAnalyticsRollupGroupsParams{
		Hourly:      q.Hourly,
		GroupBy:     groupBy,
		AppID:       pgUUID(q.AppID),
		AccountID:   pgUUID(q.AccountID),
		WindowFrom:  pgtype.Timestamptz{Time: q.From, Valid: true},
		WindowUntil: pgtype.Timestamptz{Time: q.Until, Valid: true},
		HoursFrom:   hoursFrom,
		HoursUntil:  hoursUntil,
		Route:       q.Route,
		Method:      q.Method,
		GroupLimit:  int32(q.Limit),
	})
	if err != nil {
		return nil, err
	}
	type key struct {
		bucket            int64
		dimension, method string
	}
	index := map[key]int{}
	groups := make([]Group, 0, len(rows))
	for _, row := range rows {
		k := key{row.Bucket.Time.Unix(), row.Dimension, row.Method}
		i, ok := index[k]
		if !ok {
			groups = append(groups, Group{
				Bucket: row.Bucket.Time.UTC(), Dimension: row.Dimension, Method: row.Method,
				LatencyHist: make([]int64, api.RequestAnalyticsLatencyBuckets),
			})
			i = len(groups) - 1
			index[k] = i
		}
		g := &groups[i]
		g.Requests += row.Requests
		g.ErrorRequests += row.ErrorRequests
		g.ColdBoots += row.ColdBoots
		g.RequestTimeMS += row.RequestTimeMs
		g.LatencyMaxMS = max(g.LatencyMaxMS, row.LatencyMaxMs)
		for b := range min(len(row.LatencyHist), len(g.LatencyHist)) {
			g.LatencyHist[b] += row.LatencyHist[b]
		}
	}
	return groups, nil
}

// Summary is the window's totals, from route groups read with Limit 0.
func Summary(groups []Group) sqlc.RequestTelemetryAnalyticsSummaryRow {
	total := merge(groups)
	return sqlc.RequestTelemetryAnalyticsSummaryRow{
		Requests: total.Requests, ErrorRequests: total.ErrorRequests, ColdBoots: total.ColdBoots,
		P50Ms: total.P(0.50), P95Ms: total.P(0.95), P99Ms: total.P(0.99),
	}
}

// ByDimension orders groups as the raw by-dimension query does (requests
// descending, then dimension and method) and takes each group's sampled
// measures (cold request and wake boot p95, guest execution, CPU and RSS)
// from the raw rows with the same dimension and method, when there are any.
func ByDimension(groups []Group, sampled []sqlc.RequestTelemetryAnalyticsByDimensionRow) []sqlc.RequestTelemetryAnalyticsByDimensionRow {
	type key struct{ dimension, method string }
	raw := make(map[key]sqlc.RequestTelemetryAnalyticsByDimensionRow, len(sampled))
	for _, row := range sampled {
		raw[key{asString(row.Dimension), asString(row.Method)}] = row
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Requests != groups[j].Requests {
			return groups[i].Requests > groups[j].Requests
		}
		if groups[i].Dimension != groups[j].Dimension {
			return groups[i].Dimension < groups[j].Dimension
		}
		return groups[i].Method < groups[j].Method
	})
	out := make([]sqlc.RequestTelemetryAnalyticsByDimensionRow, 0, len(groups))
	for _, g := range groups {
		row := sqlc.RequestTelemetryAnalyticsByDimensionRow{
			Dimension: g.Dimension, Method: g.Method,
			Requests: g.Requests, ErrorRequests: g.ErrorRequests, ColdBoots: g.ColdBoots,
			RequestTimeMs: g.RequestTimeMS, P50Ms: g.P(0.50), P95Ms: g.P(0.95), P99Ms: g.P(0.99),
		}
		if s, ok := raw[key{g.Dimension, g.Method}]; ok && g.Dimension != OtherDimension {
			row.ColdRequestP95Ms = s.ColdRequestP95Ms
			row.WakeBootP95Ms = s.WakeBootP95Ms
			row.GuestExecutionP50Ms = s.GuestExecutionP50Ms
			row.GuestExecutionP95Ms = s.GuestExecutionP95Ms
			row.GuestCpuAvgMs = s.GuestCpuAvgMs
			row.GuestCpuP95Ms = s.GuestCpuP95Ms
			row.GuestPeakRssMaxMb = s.GuestPeakRssMaxMb
		}
		out = append(out, row)
	}
	return out
}

// Hours lists the UTC hour buckets the raw timeseries zero-fills.
func Hours(from, until time.Time) []time.Time {
	first := from.UTC().Truncate(time.Hour)
	last := until.UTC().Add(-time.Microsecond).Truncate(time.Hour)
	var hours []time.Time
	for h := first; !h.After(last); h = h.Add(time.Hour) {
		hours = append(hours, h)
	}
	return hours
}

// Timeseries is the zero-filled hourly series of every group together.
func Timeseries(groups []Group, from, until time.Time) []sqlc.RequestTelemetryAnalyticsTimeseriesRow {
	byHour := map[int64][]Group{}
	for _, g := range groups {
		byHour[g.Bucket.Unix()] = append(byHour[g.Bucket.Unix()], g)
	}
	hours := Hours(from, until)
	out := make([]sqlc.RequestTelemetryAnalyticsTimeseriesRow, 0, len(hours))
	for _, h := range hours {
		total := merge(byHour[h.Unix()])
		out = append(out, sqlc.RequestTelemetryAnalyticsTimeseriesRow{
			BucketStart: pgtype.Timestamptz{Time: h, Valid: true},
			Requests:    total.Requests, ErrorRequests: total.ErrorRequests, ColdBoots: total.ColdBoots,
			P50Ms: total.P(0.50), P95Ms: total.P(0.95), P99Ms: total.P(0.99),
		})
	}
	return out
}

// TimeseriesGrouped zero-fills every group's hourly series, ordered by
// dimension, method and hour as the raw grouped timeseries is.
func TimeseriesGrouped(groups []Group, from, until time.Time) []sqlc.RequestTelemetryAnalyticsTimeseriesGroupedRow {
	type key struct{ dimension, method string }
	cells := map[key]map[int64]Group{}
	var keys []key
	for _, g := range groups {
		k := key{g.Dimension, g.Method}
		if cells[k] == nil {
			cells[k] = map[int64]Group{}
			keys = append(keys, k)
		}
		cells[k][g.Bucket.Unix()] = g
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dimension != keys[j].dimension {
			return keys[i].dimension < keys[j].dimension
		}
		return keys[i].method < keys[j].method
	})
	hours := Hours(from, until)
	out := make([]sqlc.RequestTelemetryAnalyticsTimeseriesGroupedRow, 0, len(keys)*len(hours))
	for _, k := range keys {
		for _, h := range hours {
			g := cells[k][h.Unix()]
			out = append(out, sqlc.RequestTelemetryAnalyticsTimeseriesGroupedRow{
				BucketStart: pgtype.Timestamptz{Time: h, Valid: true}, Dimension: k.dimension, Method: k.method,
				Requests: g.Requests, ErrorRequests: g.ErrorRequests, ColdBoots: g.ColdBoots,
				P50Ms: g.P(0.50), P95Ms: g.P(0.95), P99Ms: g.P(0.99),
			})
		}
	}
	return out
}

func merge(groups []Group) Group {
	total := Group{LatencyHist: make([]int64, api.RequestAnalyticsLatencyBuckets)}
	for _, g := range groups {
		total.Requests += g.Requests
		total.ErrorRequests += g.ErrorRequests
		total.ColdBoots += g.ColdBoots
		total.RequestTimeMS += g.RequestTimeMS
		total.LatencyMaxMS = max(total.LatencyMaxMS, g.LatencyMaxMS)
		for b := range min(len(g.LatencyHist), len(total.LatencyHist)) {
			total.LatencyHist[b] += g.LatencyHist[b]
		}
	}
	return total
}

func asString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}

func pgUUID(id string) pgtype.UUID {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}
