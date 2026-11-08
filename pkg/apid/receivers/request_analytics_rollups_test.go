package receivers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeRollupStore struct {
	err      error
	rows     []state.RequestAnalyticsRollup
	coverage []state.RequestAnalyticsCoverage
	gaps     []state.RequestAnalyticsGap
	// refuse fails, as Postgres does for bad data, any write holding a row
	// it matches.
	refuse func(state.RequestAnalyticsRollup) bool
}

func (s *fakeRollupStore) AddRequestAnalyticsRollups(_ context.Context, rows []state.RequestAnalyticsRollup, coverage []state.RequestAnalyticsCoverage) error {
	if s.err != nil {
		return s.err
	}
	for _, r := range rows {
		if s.refuse != nil && s.refuse(r) {
			return &pgconn.PgError{Code: "22021", Message: "invalid byte sequence for encoding \"UTF8\""}
		}
		if !utf8.ValidString(r.Dimension) || len(r.Dimension) > 512 || len(r.Method) > 16 {
			return &pgconn.PgError{Code: "22021", Message: "row Postgres would refuse"}
		}
	}
	s.rows = append(s.rows, rows...)
	s.coverage = append(s.coverage, coverage...)
	return nil
}

func (s *fakeRollupStore) RecordRequestAnalyticsRollupGaps(_ context.Context, gaps []state.RequestAnalyticsGap) error {
	if s.err != nil {
		return s.err
	}
	s.gaps = append(s.gaps, gaps...)
	return nil
}

func (s *fakeRollupStore) requests(kind, dimension string) int64 {
	var n int64
	for _, r := range s.rows {
		if r.DimensionKind == kind && (dimension == "" || r.Dimension == dimension) {
			n += r.Requests
		}
	}
	return n
}

func rollupTelemetryRequest(accountID, appID, deploymentID string, at time.Time) *apidpb.IncrementRequestTelemetryRequest {
	return &apidpb.IncrementRequestTelemetryRequest{
		EventId: uuid.NewString(), AccountId: accountID, AppId: appID, DeploymentId: deploymentID,
		RouteTemplate: "GET /v1/items", Method: "GET", HttpStatus: 200, LatencyMs: 42,
		ReceivedAtUnixMs: at.UnixMilli(), Count: 1, UsageOutboxed: true, Country: "DE",
	}
}

func TestRequestAnalyticsRollupsCountRowsTheIngestCapTurnsAway(t *testing.T) {
	limit := api.MustLimitsFor(api.PlanHobby).DebugTelemetryRequestsPerMinute
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanHobby}}
	rollupStore := &fakeRollupStore{}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	r := NewRequestTelemetryReceiver(store, nil, nil, true)
	rollups.applyRequestTelemetry(r)
	accountID, appID, deploymentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	sent, limited := limit+25, 0
	for range sent {
		if r.HandleOne(t.Context(), rollupTelemetryRequest(accountID, appID, deploymentID, now)).GetOutcome() == rtOutcomeRateLimited {
			limited++
		}
	}
	if limited == 0 {
		t.Fatalf("no row of %d reached the Hobby ingest cap of %d", sent, limit)
	}
	if err := flushClosed(t, rollups); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{RollupKindRoute, RollupKindCountry, RollupKindReferrerHost, RollupKindUAFamily, RollupKindConsumerID} {
		if got := rollupStore.requests(kind, ""); got != int64(sent) {
			t.Fatalf("%s rollups hold %d requests, want all %d (raw rows kept %d)", kind, got, sent, len(store.inserted))
		}
	}
	if got := rollupStore.requests(RollupKindConsumerID, rollupAnonymousConsumer); got != int64(sent) {
		t.Fatalf("anonymous consumer rollup = %d, want %d", got, sent)
	}
	if len(rollupStore.coverage) != 1 || rollupStore.coverage[0].AppID.String() != appID ||
		!rollupStore.coverage[0].CoveredSince.Equal(now.Truncate(time.Minute)) {
		t.Fatalf("coverage = %+v, want the app's first minute", rollupStore.coverage)
	}
	// Coverage is recorded once per process.
	r.HandleOne(t.Context(), rollupTelemetryRequest(accountID, appID, deploymentID, now))
	rollupStore.coverage = nil
	if err := flushClosed(t, rollups); err != nil || len(rollupStore.coverage) != 0 {
		t.Fatalf("second flush coverage = %+v err %v, want none", rollupStore.coverage, err)
	}
}

func TestRequestAnalyticsRollupsSkipPlansWithoutTelemetryAndReplays(t *testing.T) {
	rollupStore := &fakeRollupStore{}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	free := NewRequestTelemetryReceiver(&consumerTelemetryStore{account: state.Account{Plan: api.PlanFree}}, nil, nil, true)
	rollups.applyRequestTelemetry(free)
	now := time.Now().UTC()
	if out := free.HandleOne(t.Context(), rollupTelemetryRequest(uuid.NewString(), uuid.NewString(), uuid.NewString(), now)); out.GetOutcome() != rtOutcomeNotRetained {
		t.Fatalf("free outcome = %q", out.GetOutcome())
	}
	pro := NewRequestTelemetryReceiver(&consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}, nil, nil, true)
	rollups.applyRequestTelemetry(pro)
	req := rollupTelemetryRequest(uuid.NewString(), uuid.NewString(), uuid.NewString(), now)
	req.Count = 4
	pro.HandleOne(t.Context(), req)
	pro.HandleOne(t.Context(), req) // the gateway replays a batch whose response it missed
	if err := flushClosed(t, rollups); err != nil {
		t.Fatal(err)
	}
	if got := rollupStore.requests(RollupKindRoute, ""); got != 4 {
		t.Fatalf("route rollup = %d requests, want the Pro row's 4 once and no Free row", got)
	}
}

func TestRequestAnalyticsRollupsFoldDimensionsPastTheBound(t *testing.T) {
	rollupStore := &fakeRollupStore{}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	app, account, deployment := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	extra := 7
	for i := range api.RequestAnalyticsRollupDimensionsMax + extra {
		rollups.Add(RequestAnalyticsRollupRow{
			EventID: uuid.New(), AccountID: account, AppID: app, DeploymentID: deployment, ReceivedAt: now,
			Route: fmt.Sprintf("GET /r%d", i), Method: "GET", Status: 500, LatencyMS: 120_000, Count: 1,
			Country: fmt.Sprintf("C%d", i), ColdBoot: i == 0,
		})
	}
	if err := flushClosed(t, rollups); err != nil {
		t.Fatal(err)
	}
	countries, routes := 0, 0
	for _, r := range rollupStore.rows {
		switch r.DimensionKind {
		case RollupKindCountry:
			countries++
		case RollupKindRoute:
			routes++
		}
		if r.ErrorRequests != r.Requests || r.LatencyHist[api.RequestAnalyticsLatencyBuckets-1] != r.Requests || r.LatencyMaxMS != 120_000 {
			t.Fatalf("row %+v: want every request a 5xx error in the overflow bucket", r)
		}
	}
	if countries != api.RequestAnalyticsRollupDimensionsMax+1 || rollupStore.requests(RollupKindCountry, rollupOtherDimension) != int64(extra) {
		t.Fatalf("country rows = %d, __other__ = %d; want %d rows with %d folded", countries,
			rollupStore.requests(RollupKindCountry, rollupOtherDimension), api.RequestAnalyticsRollupDimensionsMax+1, extra)
	}
	if routes != api.RequestAnalyticsRollupDimensionsMax+extra {
		t.Fatalf("route rows = %d, want every route kept", routes)
	}
}

func TestRequestAnalyticsRollupsKeepRowsWhenAFlushFails(t *testing.T) {
	rollupStore := &fakeRollupStore{err: errors.New("postgres down")}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	app, account, deployment := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	add := func(n int) {
		rollups.Add(RequestAnalyticsRollupRow{EventID: uuid.New(), AccountID: account, AppID: app, DeploymentID: deployment,
			ReceivedAt: now, Route: "GET /", Method: "GET", Status: 200, LatencyMS: 10, Count: n})
	}
	add(2)
	if err := flushClosed(t, rollups); err == nil {
		t.Fatal("flush against a failing store succeeded")
	}
	add(3)
	rollupStore.err = nil
	if err := flushClosed(t, rollups); err != nil {
		t.Fatal(err)
	}
	if got := rollupStore.requests(RollupKindRoute, ""); got != 5 || len(rollupStore.coverage) != 1 {
		t.Fatalf("route rollup = %d, coverage %d; want 5 requests in one row and the coverage kept", got, len(rollupStore.coverage))
	}
}

// flushClosed flushes as if every pending minute had closed past its grace.
func flushClosed(t *testing.T, a *RequestAnalyticsRollups) error {
	t.Helper()
	a.now = func() time.Time { return time.Now().Add(time.Hour) }
	return a.Flush(t.Context())
}

// Non-route rows are keyed by value alone (amendment 3): no reader splits
// them by status or deployment, and keying them by both multiplied them.
func TestRequestAnalyticsRollupsKeyDimensionRowsByValueAlone(t *testing.T) {
	rollupStore := &fakeRollupStore{}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	app, account := uuid.New(), uuid.New()
	deployments := []uuid.UUID{uuid.New(), uuid.New()}
	now := time.Now().UTC()
	statuses := []int{200, 201, 404, 500}
	for i := range 40 {
		rollups.Add(RequestAnalyticsRollupRow{
			EventID: uuid.New(), AccountID: account, AppID: app, DeploymentID: deployments[(i/4)%2], ReceivedAt: now,
			Route: "GET /", Method: "GET", Status: statuses[i%4], LatencyMS: 10, Count: 1,
			Country: "DE", ReferrerHost: "example.com", UAFamily: "Chrome", ConsumerID: "c1",
		})
	}
	if err := flushClosed(t, rollups); err != nil {
		t.Fatal(err)
	}
	perKind := map[string]int{}
	for _, r := range rollupStore.rows {
		perKind[r.DimensionKind]++
		if r.DimensionKind == RollupKindRoute {
			if r.Status < 100 || r.DeploymentID == uuid.Nil {
				t.Fatalf("route row %+v: want its status and deployment", r)
			}
			continue
		}
		if r.Status != rollupFoldedStatus || r.DeploymentID != uuid.Nil || r.Requests != 40 || r.ErrorRequests != 20 {
			t.Fatalf("%s row %+v: want one status-0, nil-deployment row of all 40 requests, 20 of them errors", r.DimensionKind, r)
		}
	}
	if perKind[RollupKindRoute] != 8 {
		t.Fatalf("route rows = %d, want 8 (4 statuses x 2 deployments)", perKind[RollupKindRoute])
	}
	for _, kind := range rollupFoldedKinds {
		if perKind[kind] != 1 {
			t.Fatalf("%s rows = %d, want 1", kind, perKind[kind])
		}
	}
}

// A minute is written once it has closed past the grace, with one top-N
// fold over all of it: a value busy late in the minute is not folded away by
// an earlier, quieter flush, and an open minute waits.
func TestRequestAnalyticsRollupsWriteAMinuteOnceItCloses(t *testing.T) {
	rollupStore := &fakeRollupStore{}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	app, account, deployment := uuid.New(), uuid.New(), uuid.New()
	minute := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	clock := minute.Add(10 * time.Second)
	rollups.now = func() time.Time { return clock }
	add := func(at time.Time, consumer string, n int) {
		rollups.Add(RequestAnalyticsRollupRow{
			EventID: uuid.New(), AccountID: account, AppID: app, DeploymentID: deployment, ReceivedAt: at,
			Route: "GET /", Method: "GET", Status: 200, LatencyMS: 10, Count: n, ConsumerID: consumer,
		})
	}
	// Early in the minute: the 50 quiet consumers fill the top-N.
	for i := range api.RequestAnalyticsRollupDimensionsMax {
		add(minute.Add(5*time.Second), fmt.Sprintf("quiet-%d", i), 2)
	}
	for ; clock.Before(minute.Add(time.Minute)); clock = clock.Add(api.RequestAnalyticsRollupFlushInterval) {
		if err := rollups.Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// Late in the minute a new consumer is the busiest.
	add(minute.Add(55*time.Second), "busy", 500)
	if len(rollupStore.rows) != 0 || len(rollupStore.coverage) != 0 {
		t.Fatalf("wrote %d rows, %d coverage before the minute closed", len(rollupStore.rows), len(rollupStore.coverage))
	}
	clock = minute.Add(time.Minute + api.RequestAnalyticsRollupMinuteGrace)
	if err := rollups.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	consumers := 0
	for _, r := range rollupStore.rows {
		if r.DimensionKind == RollupKindConsumerID {
			consumers++
		}
	}
	if got := rollupStore.requests(RollupKindConsumerID, "busy"); got != 500 || consumers != api.RequestAnalyticsRollupDimensionsMax+1 {
		t.Fatalf("busy consumer = %d requests in %d consumer rows; want its 500 kept among %d rows", got, consumers, api.RequestAnalyticsRollupDimensionsMax+1)
	}
	if len(rollupStore.coverage) != 1 || !rollupStore.coverage[0].CoveredSince.Equal(minute) {
		t.Fatalf("coverage = %+v, want the closed minute", rollupStore.coverage)
	}
	// A straggler for the written minute goes out with the next flush.
	add(minute.Add(59*time.Second), "busy", 1)
	clock = clock.Add(api.RequestAnalyticsRollupFlushInterval)
	if err := rollups.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := rollupStore.requests(RollupKindRoute, ""); got != 2*api.RequestAnalyticsRollupDimensionsMax+501 {
		t.Fatalf("route requests = %d, want every request once", got)
	}
}

// Shutdown writes the open minute too.
func TestRequestAnalyticsRollupsFinalFlushWritesOpenMinutes(t *testing.T) {
	rollupStore := &fakeRollupStore{}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	rollups.Add(RequestAnalyticsRollupRow{
		EventID: uuid.New(), AccountID: uuid.New(), AppID: uuid.New(), DeploymentID: uuid.New(), ReceivedAt: time.Now(),
		Route: "GET /", Method: "GET", Status: 200, LatencyMS: 10, Count: 3,
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rollups.Run(ctx, time.Hour)
	if got := rollupStore.requests(RollupKindRoute, ""); got != 3 {
		t.Fatalf("route requests after shutdown = %d, want 3", got)
	}
}

func TestRequestAnalyticsLatencyBucketBounds(t *testing.T) {
	if api.RequestAnalyticsLatencyBuckets != 45 {
		t.Fatalf("latency_hist width = %d; the migration CHECK and AddRequestAnalyticsMinutes slice 45", api.RequestAnalyticsLatencyBuckets)
	}
	for _, tc := range []struct{ ms, bucket int }{
		{-1, 0}, {0, 0}, {10, 0}, {11, 1}, {100, 9}, {101, 10}, {60_000, 43}, {60_001, 44},
	} {
		if got := requestAnalyticsLatencyBucket(tc.ms); got != tc.bucket {
			t.Fatalf("bucket(%d) = %d, want %d", tc.ms, got, tc.bucket)
		}
	}
}
