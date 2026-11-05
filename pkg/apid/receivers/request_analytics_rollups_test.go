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
	if err := rollups.Flush(t.Context()); err != nil {
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
	if err := rollups.Flush(t.Context()); err != nil || len(rollupStore.coverage) != 0 {
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
	if err := rollups.Flush(t.Context()); err != nil {
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
	if err := rollups.Flush(t.Context()); err != nil {
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
	if err := rollups.Flush(t.Context()); err == nil {
		t.Fatal("flush against a failing store succeeded")
	}
	add(3)
	rollupStore.err = nil
	if err := rollups.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := rollupStore.requests(RollupKindRoute, ""); got != 5 || len(rollupStore.coverage) != 1 {
		t.Fatalf("route rollup = %d, coverage %d; want 5 requests in one row and the coverage kept", got, len(rollupStore.coverage))
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
