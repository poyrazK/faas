//go:build !no_pg

package state_test

// ADR-127 request analytics run only on Postgres: MemStore answers every
// request_telemetry read with a sentinel error, and the apid handler tests
// inject stub stores, so these queries had no test that executed them. The
// fixtures below use hand-computed, request-weighted expectations — each
// telemetry row can stand for several requests (count > 1).

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type telemetryFixture struct {
	s       *state.PgStore
	ctx     context.Context
	account pgtype.UUID
	app     pgtype.UUID
	dep     pgtype.UUID
}

func mustPgUUID(t *testing.T, id string) pgtype.UUID {
	t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", id, err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

func newTelemetryFixture(t *testing.T) telemetryFixture {
	t.Helper()
	s, ctx := pgStore(t)
	acct, app, dep := seedLiveDeploy(t, s, ctx)
	return telemetryFixture{s: s, ctx: ctx, account: mustPgUUID(t, acct), app: mustPgUUID(t, app), dep: mustPgUUID(t, dep)}
}

type telemetryRow struct {
	at      time.Time
	route   string
	method  string
	status  int32
	latency int32
	count   int32
	cold    bool
	country string
}

func (f telemetryFixture) insert(t *testing.T, rows ...telemetryRow) {
	t.Helper()
	for _, r := range rows {
		country := r.country
		if country == "" {
			country = "__unknown__"
		}
		if err := f.s.InsertRequestTelemetry(f.ctx, sqlc.InsertRequestTelemetryParams{
			AccountID: f.account, AppID: f.app, DeploymentID: f.dep,
			Route: r.route, Method: r.method, Status: r.status, LatencyMs: r.latency,
			ColdBoot: r.cold, Count: r.count,
			ReceivedAt: pgtype.Timestamptz{Time: r.at, Valid: true},
			Country:    country, UaFamily: "__unknown__", ReferrerHost: "__unknown__",
			GuestRuntime: "__unknown__", GuestOutcome: "missing",
		}); err != nil {
			t.Fatalf("InsertRequestTelemetry(%+v): %v", r, err)
		}
	}
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// analyticsFixture seeds seven requests across two hours:
//
//	10:15 GET  /a 200 10ms ×3
//	10:25 GET  /a 500 100ms ×1
//	11:05 POST /b 200 50ms ×2 cold
//	11:20 GET  /c 404 5ms ×1 country=DE
//	09:59 GET  /a 200 1ms ×9   (before the window)
//	12:00 GET  /a 200 1ms ×9   (at the exclusive window end)
func analyticsFixture(t *testing.T) (telemetryFixture, time.Time, time.Time) {
	t.Helper()
	f := newTelemetryFixture(t)
	base := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	f.insert(t,
		telemetryRow{at: base.Add(15 * time.Minute), route: "/a", method: "GET", status: 200, latency: 10, count: 3},
		telemetryRow{at: base.Add(25 * time.Minute), route: "/a", method: "GET", status: 500, latency: 100, count: 1},
		telemetryRow{at: base.Add(65 * time.Minute), route: "/b", method: "POST", status: 200, latency: 50, count: 2, cold: true},
		telemetryRow{at: base.Add(80 * time.Minute), route: "/c", method: "GET", status: 404, latency: 5, count: 1, country: "DE"},
		telemetryRow{at: base.Add(-time.Minute), route: "/a", method: "GET", status: 200, latency: 1, count: 9},
		telemetryRow{at: base.Add(2 * time.Hour), route: "/a", method: "GET", status: 200, latency: 1, count: 9},
	)
	return f, base, base.Add(2 * time.Hour)
}

func TestPgRequestTelemetryAnalytics_SummaryIsRequestWeighted(t *testing.T) {
	f, since, until := analyticsFixture(t)
	got, err := f.s.RequestTelemetryAnalyticsSummary(f.ctx, sqlc.RequestTelemetryAnalyticsSummaryParams{
		AppID: f.app, AccountID: f.account, ReceivedAt: ts(since), ReceivedAt_2: ts(until),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Latencies weighted by count: 5×1, 10×3, 50×2, 100×1 (n=7).
	want := sqlc.RequestTelemetryAnalyticsSummaryRow{Requests: 7, ErrorRequests: 2, ColdBoots: 2, P50Ms: 10, P95Ms: 100, P99Ms: 100}
	if got.Requests != want.Requests || got.ErrorRequests != want.ErrorRequests || got.ColdBoots != want.ColdBoots ||
		got.P50Ms != want.P50Ms || got.P95Ms != want.P95Ms || got.P99Ms != want.P99Ms {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}

	empty, err := f.s.RequestTelemetryAnalyticsSummary(f.ctx, sqlc.RequestTelemetryAnalyticsSummaryParams{
		AppID: f.app, AccountID: f.account, ReceivedAt: ts(until.Add(time.Hour)), ReceivedAt_2: ts(until.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("empty window: %v", err)
	}
	if empty.Requests != 0 || empty.P95Ms != 0 {
		t.Fatalf("empty window summary = %+v, want zeros", empty)
	}
}

func TestPgRequestTelemetryAnalytics_ByRouteOrdersAndLimits(t *testing.T) {
	f, since, until := analyticsFixture(t)
	rows, err := f.s.RequestTelemetryAnalyticsByRoute(f.ctx, sqlc.RequestTelemetryAnalyticsByRouteParams{
		AppID: f.app, AccountID: f.account, ReceivedAt: ts(since), ReceivedAt_2: ts(until), Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want the top 2 routes", rows)
	}
	a, b := rows[0], rows[1]
	if a.Route != "/a" || a.Method != "GET" || a.Requests != 4 || a.ErrorRequests != 1 || a.P50Ms != 10 || a.P95Ms != 100 {
		t.Fatalf("top route = %+v, want GET /a: 4 requests, 1 error, p50 10, p95 100", a)
	}
	if b.Route != "/b" || b.Method != "POST" || b.Requests != 2 || b.ColdBoots != 2 || b.P50Ms != 50 {
		t.Fatalf("second route = %+v, want POST /b: 2 requests, 2 cold, p50 50", b)
	}
}

func TestPgRequestTelemetryAnalytics_TimeseriesFillsEveryHour(t *testing.T) {
	f, since, until := analyticsFixture(t)
	rows, err := f.s.RequestTelemetryAnalyticsTimeseries(f.ctx, sqlc.RequestTelemetryAnalyticsTimeseriesParams{
		AppID: f.app, AccountID: f.account, ReceivedAt: ts(since), ReceivedAt2: ts(until),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("buckets = %d (%+v), want exactly the 10:00 and 11:00 hours of [10:00, 12:00)", len(rows), rows)
	}
	first, second := rows[0], rows[1]
	if !first.BucketStart.Time.Equal(since) || first.Requests != 4 || first.ErrorRequests != 1 || first.P50Ms != 10 {
		t.Fatalf("10:00 bucket = %+v", first)
	}
	if !second.BucketStart.Time.Equal(since.Add(time.Hour)) || second.Requests != 3 || second.ColdBoots != 2 || second.P50Ms != 50 {
		t.Fatalf("11:00 bucket = %+v", second)
	}

	// An hour with no traffic is still a bucket, zero-filled.
	gap, err := f.s.RequestTelemetryAnalyticsTimeseries(f.ctx, sqlc.RequestTelemetryAnalyticsTimeseriesParams{
		AppID: f.app, AccountID: f.account, ReceivedAt: ts(since.Add(-3 * time.Hour)), ReceivedAt2: ts(since.Add(-time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(gap) != 2 || gap[0].Requests != 0 || gap[1].Requests != 0 {
		t.Fatalf("quiet window = %+v, want two zero buckets", gap)
	}
}

func TestPgRequestTelemetryAnalytics_ByDimensionFoldsTheTailIntoOther(t *testing.T) {
	f, since, until := analyticsFixture(t)
	rows, err := f.s.RequestTelemetryAnalyticsByDimension(f.ctx, sqlc.RequestTelemetryAnalyticsByDimensionParams{
		AppID: f.app, AccountID: f.account, ReceivedAt: ts(since), ReceivedAt_2: ts(until), GroupBy: "country", Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	byDim := map[string]sqlc.RequestTelemetryAnalyticsByDimensionRow{}
	var total int64
	for _, r := range rows {
		byDim[stringValue(r.Dimension)] = r
		total += r.Requests
	}
	if total != 7 {
		t.Fatalf("groups = %+v; requests sum to %d, want 7 (every request lands in exactly one group)", rows, total)
	}
	if byDim["__unknown__"].Requests != 6 || byDim["__other__"].Requests != 1 {
		t.Fatalf("groups = %+v, want __unknown__=6 and the DE tail folded into __other__=1", rows)
	}
}

func TestPgRequestTelemetryAnalytics_TimeseriesGroupedKeepsEveryGroupInEveryBucket(t *testing.T) {
	f, since, until := analyticsFixture(t)
	rows, err := f.s.RequestTelemetryAnalyticsTimeseriesGrouped(f.ctx, sqlc.RequestTelemetryAnalyticsTimeseriesGroupedParams{
		AppID: f.app, AccountID: f.account, ReceivedAt: ts(since), ReceivedAt2: ts(until), GroupBy: "route", Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		dim, method string
		hour        int
	}
	got := map[key]int64{}
	for _, r := range rows {
		got[key{r.Dimension, r.Method, r.BucketStart.Time.UTC().Hour()}] = r.Requests
	}
	want := map[key]int64{
		{"/a", "GET", 10}: 4, {"/a", "GET", 11}: 0,
		{"__other__", "", 10}: 0, {"__other__", "", 11}: 3,
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %d (2 groups × 2 buckets)", rows, len(want))
	}
	for k, v := range want {
		if n, ok := got[k]; !ok || n != v {
			t.Fatalf("%+v = %d (present=%v), want %d; rows = %+v", k, n, ok, v, rows)
		}
	}
}

func stringValue(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return ""
	}
}

// TestPgListRequestTelemetryByApp_PagesTiesAndFilters walks the debug
// telemetry list the way apid does: keyset on (received_at, id) descending,
// with every filter at its "off" sentinel except the one under test.
func TestPgListRequestTelemetryByApp_PagesTiesAndFilters(t *testing.T) {
	f := newTelemetryFixture(t)
	at := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	// Five rows at the identical instant: the id half of the cursor must
	// carry the page walk through the tie.
	for i := 0; i < 5; i++ {
		f.insert(t, telemetryRow{at: at, route: "/tie", method: "GET", status: 200, latency: int32(10 + i), count: 1})
	}
	f.insert(t,
		telemetryRow{at: at.Add(time.Minute), route: "/slow", method: "GET", status: 503, latency: 900, count: 1, cold: true},
		telemetryRow{at: at.Add(2 * time.Minute), route: "/fast", method: "POST", status: 201, latency: 3, count: 1},
	)
	base := sqlc.ListRequestTelemetryByAppParams{
		AppID: f.app, ReceivedAt: ts(at.Add(-time.Hour)), ReceivedAt_2: ts(at.Add(time.Hour)),
		ColdBootFilter: -1, Limit: 2,
	}

	seen := map[string]int{}
	params := base
	for page := 0; page < 10; page++ {
		rows, err := f.s.ListRequestTelemetryByApp(f.ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			seen[uuid.UUID(r.ID.Bytes).String()]++
		}
		if len(rows) < int(params.Limit) {
			break
		}
		last := rows[len(rows)-1]
		params.CursorReceivedAt = last.ReceivedAt
		params.CursorID = last.ID
	}
	if len(seen) != 7 {
		t.Fatalf("walked %d distinct rows, want all 7", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("row %s listed %d times across pages", id, n)
		}
	}

	count := func(p sqlc.ListRequestTelemetryByAppParams) int {
		t.Helper()
		p.Limit = 100
		rows, err := f.s.ListRequestTelemetryByApp(f.ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	cases := []struct {
		name string
		edit func(*sqlc.ListRequestTelemetryByAppParams)
		want int
	}{
		{"route", func(p *sqlc.ListRequestTelemetryByAppParams) { p.Route = "/tie" }, 5},
		{"status", func(p *sqlc.ListRequestTelemetryByAppParams) { p.StatusFilter = 503 }, 1},
		{"cold only", func(p *sqlc.ListRequestTelemetryByAppParams) { p.ColdBootFilter = 1 }, 1},
		{"warm only", func(p *sqlc.ListRequestTelemetryByAppParams) { p.ColdBootFilter = 0 }, 6},
		{"min latency", func(p *sqlc.ListRequestTelemetryByAppParams) { p.MinLatencyMs = 14 }, 2},
		{"deployment", func(p *sqlc.ListRequestTelemetryByAppParams) { p.DeploymentID = uuid.UUID(f.dep.Bytes).String() }, 7},
		{"other deployment", func(p *sqlc.ListRequestTelemetryByAppParams) { p.DeploymentID = uuid.NewString() }, 0},
		{"anonymous consumer", func(p *sqlc.ListRequestTelemetryByAppParams) { p.ConsumerAnonymous = true }, 7},
		{"named consumer", func(p *sqlc.ListRequestTelemetryByAppParams) { p.ConsumerID = uuid.NewString() }, 0},
	}
	for _, tc := range cases {
		p := base
		tc.edit(&p)
		if got := count(p); got != tc.want {
			t.Errorf("%s: %d rows, want %d", tc.name, got, tc.want)
		}
	}
}

// TestPgGetRequestTelemetryByAppAndIdentifier resolves a request by its row
// id or trace id, preferring an exact id match, and never across apps.
func TestPgGetRequestTelemetryByAppAndIdentifier(t *testing.T) {
	f := newTelemetryFixture(t)
	at := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	trace := "4bf92f3577b34da6a3ce929d0e0e4736"
	if err := f.s.InsertRequestTelemetry(f.ctx, sqlc.InsertRequestTelemetryParams{
		AccountID: f.account, AppID: f.app, DeploymentID: f.dep, Route: "/t", Method: "GET",
		Status: 200, LatencyMs: 7, Count: 1, ReceivedAt: ts(at), TraceID: pgtype.Text{String: trace, Valid: true},
		Country: "__unknown__", UaFamily: "__unknown__", ReferrerHost: "__unknown__",
		GuestRuntime: "__unknown__", GuestOutcome: "missing",
	}); err != nil {
		t.Fatal(err)
	}
	window := func(id string, app pgtype.UUID) (sqlc.GetRequestTelemetryByAppAndIdentifierRow, error) {
		return f.s.GetRequestTelemetryByAppAndIdentifier(f.ctx, sqlc.GetRequestTelemetryByAppAndIdentifierParams{
			AppID: app, Identifier: id, ReceivedFrom: ts(at.Add(-time.Hour)), ReceivedUntil: ts(at.Add(time.Hour)),
		})
	}
	byTrace, err := window(trace, f.app)
	if err != nil || byTrace.Route != "/t" {
		t.Fatalf("by trace id: %+v, %v", byTrace, err)
	}
	byID, err := window(uuid.UUID(byTrace.ID.Bytes).String(), f.app)
	if err != nil || byID.ID != byTrace.ID {
		t.Fatalf("by row id: %+v, %v", byID, err)
	}
	other := newTelemetryFixture(t)
	if _, err := window(trace, other.app); err == nil {
		t.Fatal("a trace id resolved under another app")
	}
}
