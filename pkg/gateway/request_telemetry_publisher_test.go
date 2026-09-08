// request_telemetry_publisher_test.go — table-driven tests for the
// PR-B collapseRequestTelemetry aggregate.
// adr: 127
//
// The PR-A pass-through behavior is gone: every row drained from the
// recorder is now collapsed by
// (app_id, deployment_id, route, method, status, dimensions,
// minute_bucket, latency_bucket) into one row with Count = the number
// of originals that folded into the bucket. These tests pin the shape:
//
//   - 1000 rows in one latency bucket → 1 collapsed row with Count=1000
//   - 2 distinct routes → 2 collapsed rows with Count=100 each
//   - rows straddling a minute boundary DO NOT fold together
//   - latency buckets preserve distinct portions of the distribution
//   - ColdBoot OR: any cold row in the bucket → ColdBoot=true
//   - TraceID: first non-empty wins
//   - ReceivedAt is truncated to the minute bucket boundary
//   - Count is clamped to >= 1 (defends against recorder-side bugs
//     that leave Count=0; the schema CHECK rejects 0 with SQLSTATE
//     23514)
//
// These tests live in package gateway so they can exercise the
// unexported collapse function directly without spinning the
// publisher goroutine.

package gateway

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// makeCollapseRow is the test-side row factory for collapse tests.
// All UUIDs default to stable test values so table-driven cases
// can mix-and-match keys. (Named makeCollapseRow to avoid collision
// with the existing makeRow helper in request_telemetry_test.go
// which has a different signature.)
func makeCollapseRow(accountID, appID, deployID uuid.UUID, route, method string, status, latencyMS int, coldBoot bool, traceID string, ts time.Time) RequestTelemetryRow {
	return RequestTelemetryRow{
		AccountID:    accountID,
		AppID:        appID,
		DeploymentID: deployID,
		Route:        route,
		Method:       method,
		Status:       status,
		LatencyMS:    latencyMS,
		ColdBoot:     coldBoot,
		TraceID:      traceID,
		ReceivedAt:   ts,
		Count:        1,
	}
}

func TestCollapseRequestTelemetry_BulkFoldsIntoOneBucket(t *testing.T) {
	t.Parallel()
	appID := uuid.New()
	deployID := uuid.New()
	accountID := uuid.New()
	// All 1000 rows land in the same minute and latency bucket.
	base := time.Date(2026, 8, 24, 18, 42, 7, 0, time.UTC)
	rows := make([]RequestTelemetryRow, 1000)
	for i := range rows {
		rows[i] = makeCollapseRow(accountID, appID, deployID,
			"GET /v1/checkout/{id}", "GET", 200, 12+i%5, false,
			"trace"+uuid.NewString()[:8], base.Add(time.Duration(i)*time.Millisecond))
	}

	collapsed := collapseRequestTelemetry(rows)
	if got, want := len(collapsed), 1; got != want {
		t.Fatalf("len(collapsed) = %d, want %d", got, want)
	}
	if got, want := collapsed[0].Count, 1000; got != want {
		t.Errorf("Count = %d, want %d", got, want)
	}
	if got, want := collapsed[0].LatencyMS, 20; got != want {
		t.Errorf("LatencyMS = %d, want %d (latency bucket upper bound)", got, want)
	}
	if collapsed[0].ColdBoot {
		t.Errorf("ColdBoot = true, want false (no cold row in bucket)")
	}
	// ReceivedAt truncated to the minute boundary.
	if got, want := collapsed[0].ReceivedAt, base.Truncate(time.Minute); got != want {
		t.Errorf("ReceivedAt = %v, want %v", got, want)
	}
}

func TestCollapseRequestTelemetry_PreservesLatencyDistribution(t *testing.T) {
	t.Parallel()
	appID := uuid.New()
	deployID := uuid.New()
	accountID := uuid.New()
	base := time.Date(2026, 8, 24, 18, 42, 0, 0, time.UTC)
	rows := make([]RequestTelemetryRow, 0, 100)
	for i := 0; i < 95; i++ {
		rows = append(rows, makeCollapseRow(accountID, appID, deployID,
			"GET /v1/foo", "GET", 200, 20, false, "", base))
	}
	for i := 0; i < 5; i++ {
		rows = append(rows, makeCollapseRow(accountID, appID, deployID,
			"GET /v1/foo", "GET", 200, 800, false, "", base))
	}

	collapsed := collapseRequestTelemetry(rows)
	if got, want := len(collapsed), 2; got != want {
		t.Fatalf("len(collapsed) = %d, want %d latency buckets", got, want)
	}
	counts := make(map[int]int)
	for _, row := range collapsed {
		counts[row.LatencyMS] = row.Count
	}
	if got, want := counts[20], 95; got != want {
		t.Errorf("20ms bucket count = %d, want %d", got, want)
	}
	if got, want := counts[800], 5; got != want {
		t.Errorf("800ms bucket count = %d, want %d", got, want)
	}
}

func TestRequestTelemetryLatencyBucketUpperBound(t *testing.T) {
	t.Parallel()
	cases := []struct {
		latency, want int
	}{
		{0, 0},
		{1, 10},
		{10, 10},
		{11, 20},
		{999, 1_000},
		{1_001, 1_050},
		{5_001, 5_250},
		{10_001, 11_000},
		{30_001, 35_000},
	}
	for _, tc := range cases {
		if got := requestTelemetryLatencyBucketUpperBound(tc.latency); got != tc.want {
			t.Errorf("requestTelemetryLatencyBucketUpperBound(%d) = %d, want %d", tc.latency, got, tc.want)
		}
	}
}

func TestCollapseRequestTelemetry_LatencyCardinalityIsBounded(t *testing.T) {
	t.Parallel()
	appID := uuid.New()
	deployID := uuid.New()
	accountID := uuid.New()
	base := time.Date(2026, 8, 24, 18, 42, 0, 0, time.UTC)
	rows := make([]RequestTelemetryRow, 60_000)
	for i := range rows {
		rows[i] = makeCollapseRow(accountID, appID, deployID,
			"GET /v1/foo", "GET", 200, i, false, "", base)
	}

	collapsed := collapseRequestTelemetry(rows)
	// A full minute of millisecond values must not become one row per
	// request. The configured bucket schedule has fewer than 250 possible
	// representatives over this range.
	if len(collapsed) >= 250 {
		t.Fatalf("len(collapsed) = %d, want fewer than 250 bounded latency buckets", len(collapsed))
	}
	var represented int
	for _, row := range collapsed {
		represented += row.Count
	}
	if represented != len(rows) {
		t.Fatalf("represented request count = %d, want %d", represented, len(rows))
	}
}

func TestCollapseRequestTelemetry_DistinctRoutesDoNotFold(t *testing.T) {
	t.Parallel()
	appID := uuid.New()
	deployID := uuid.New()
	accountID := uuid.New()
	base := time.Date(2026, 8, 24, 18, 42, 7, 0, time.UTC)
	var rows []RequestTelemetryRow
	for i := 0; i < 100; i++ {
		rows = append(rows, makeCollapseRow(accountID, appID, deployID,
			"GET /v1/checkout/{id}", "GET", 200, 12, false, "",
			base.Add(time.Duration(i)*time.Millisecond)))
	}
	for i := 0; i < 100; i++ {
		rows = append(rows, makeCollapseRow(accountID, appID, deployID,
			"POST /v1/login", "POST", 200, 18, false, "",
			base.Add(time.Duration(i)*time.Millisecond)))
	}

	collapsed := collapseRequestTelemetry(rows)
	if got, want := len(collapsed), 2; got != want {
		t.Fatalf("len(collapsed) = %d, want %d", got, want)
	}
	// Both should have Count=100.
	for i, row := range collapsed {
		if got, want := row.Count, 100; got != want {
			t.Errorf("collapsed[%d].Count = %d, want %d", i, got, want)
		}
	}
}

func TestCollapseRequestTelemetry_MinuteBoundarySplitsBuckets(t *testing.T) {
	t.Parallel()
	appID := uuid.New()
	deployID := uuid.New()
	accountID := uuid.New()
	// Two rows 30 seconds apart but in different minutes —
	// they MUST fold into separate buckets.
	t0 := time.Date(2026, 8, 24, 18, 42, 7, 0, time.UTC)
	t1 := time.Date(2026, 8, 24, 18, 43, 7, 0, time.UTC)
	rows := []RequestTelemetryRow{
		makeCollapseRow(accountID, appID, deployID, "GET /v1/foo", "GET", 200, 12, false, "", t0),
		makeCollapseRow(accountID, appID, deployID, "GET /v1/foo", "GET", 200, 18, false, "", t1),
	}

	collapsed := collapseRequestTelemetry(rows)
	if got, want := len(collapsed), 2; got != want {
		t.Fatalf("len(collapsed) = %d, want %d", got, want)
	}
}

func TestCollapseRequestTelemetry_ColdBootOR(t *testing.T) {
	t.Parallel()
	appID := uuid.New()
	deployID := uuid.New()
	accountID := uuid.New()
	base := time.Date(2026, 8, 24, 18, 42, 0, 0, time.UTC)
	// 5 rows: first 4 warm, last 1 cold.
	var rows []RequestTelemetryRow
	for i := 0; i < 4; i++ {
		rows = append(rows, makeCollapseRow(accountID, appID, deployID,
			"GET /v1/foo", "GET", 200, 12, false, "", base))
	}
	rows = append(rows, makeCollapseRow(accountID, appID, deployID,
		"GET /v1/foo", "GET", 200, 12, true, "", base))

	collapsed := collapseRequestTelemetry(rows)
	if got, want := len(collapsed), 1; got != want {
		t.Fatalf("len(collapsed) = %d, want %d", got, want)
	}
	if !collapsed[0].ColdBoot {
		t.Errorf("ColdBoot = false, want true (OR semantics)")
	}
}

func TestCollapseRequestTelemetry_TraceIDFirstNonEmpty(t *testing.T) {
	t.Parallel()
	appID := uuid.New()
	deployID := uuid.New()
	accountID := uuid.New()
	base := time.Date(2026, 8, 24, 18, 42, 0, 0, time.UTC)
	// First 3 rows have empty trace_id; 4th row has one. The
	// aggregate should carry the 4th row's trace_id (first
	// non-empty in iteration order).
	rows := []RequestTelemetryRow{
		makeCollapseRow(accountID, appID, deployID, "GET /v1/foo", "GET", 200, 12, false, "", base),
		makeCollapseRow(accountID, appID, deployID, "GET /v1/foo", "GET", 200, 12, false, "", base),
		makeCollapseRow(accountID, appID, deployID, "GET /v1/foo", "GET", 200, 12, false, "", base),
		makeCollapseRow(accountID, appID, deployID, "GET /v1/foo", "GET", 200, 12, false, "trace-abc", base),
	}

	collapsed := collapseRequestTelemetry(rows)
	if got, want := collapsed[0].TraceID, "trace-abc"; got != want {
		t.Errorf("TraceID = %q, want %q (first non-empty wins)", got, want)
	}
}

func TestCollapseRequestTelemetry_CountClampedToAtLeastOne(t *testing.T) {
	t.Parallel()
	appID := uuid.New()
	deployID := uuid.New()
	accountID := uuid.New()
	base := time.Date(2026, 8, 24, 18, 42, 0, 0, time.UTC)
	// A row with Count=0 (recorder-side bug surface) must clamp
	// to 1 at collapse time. The schema CHECK rejects 0; the
	// collapse is the last line of defense.
	row := makeCollapseRow(accountID, appID, deployID,
		"GET /v1/foo", "GET", 200, 12, false, "", base)
	row.Count = 0

	collapsed := collapseRequestTelemetry([]RequestTelemetryRow{row})
	if got, want := collapsed[0].Count, 1; got != want {
		t.Errorf("Count = %d, want %d (clamped to >= 1)", got, want)
	}
}

func TestCollapseRequestTelemetry_NilInputReturnsNil(t *testing.T) {
	t.Parallel()
	if got := collapseRequestTelemetry(nil); got != nil {
		t.Errorf("collapseRequestTelemetry(nil) = %v, want nil", got)
	}
}
