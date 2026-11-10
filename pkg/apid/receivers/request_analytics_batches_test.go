package receivers

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/requestanalytics"
	"github.com/onebox-faas/faas/pkg/state"
)

// rollupBatchTelemetryStore applies gateway batches once per batch_id, as the
// Postgres ledger does.
type rollupBatchTelemetryStore struct {
	consumerTelemetryStore
	claimed  map[uuid.UUID]bool
	rows     []state.RequestAnalyticsRollup
	coverage []state.RequestAnalyticsCoverage
}

func (s *rollupBatchTelemetryStore) AddRequestAnalyticsRollupBatch(_ context.Context, batchID uuid.UUID, _ string, rows []state.RequestAnalyticsRollup, coverage []state.RequestAnalyticsCoverage) (bool, error) {
	if s.claimed[batchID] {
		return true, nil
	}
	if s.claimed == nil {
		s.claimed = map[uuid.UUID]bool{}
	}
	s.claimed[batchID] = true
	s.rows = append(s.rows, rows...)
	s.coverage = append(s.coverage, coverage...)
	return false, nil
}

func newBatchReceiver(t *testing.T, plan api.Plan) (*requestTelemetryReceiver, *rollupBatchTelemetryStore) {
	t.Helper()
	store := &rollupBatchTelemetryStore{consumerTelemetryStore: consumerTelemetryStore{account: state.Account{Plan: plan}}}
	r := NewRequestTelemetryReceiver(store, nil, nil, true)
	NewRequestAnalyticsRollups(&fakeRollupStore{}, nil, nil).applyRequestTelemetry(r)
	return r, store
}

func routeRollupRow(accountID, appID uuid.UUID, minute time.Time, requests int64) *apidpb.RequestAnalyticsRollupRow {
	hist := make([]int64, api.RequestAnalyticsLatencyBuckets)
	hist[requestanalytics.LatencyBucket(42)] = requests
	return &apidpb.RequestAnalyticsRollupRow{
		AccountId: accountID.String(), AppId: appID.String(), DeploymentId: uuid.NewString(),
		BucketStartUnix: minute.Unix(), DimensionKind: requestanalytics.KindRoute, Dimension: "GET /v1/items",
		Method: "GET", Status: 200, Requests: requests, RequestTimeMs: 42 * requests, LatencyMaxMs: 42, LatencyHist: hist,
	}
}

// A batch is applied once: resending it after a lost answer adds nothing.
// Repeated keys in one batch merge, and coverage starts at the app's first
// minute.
func TestAddRequestAnalyticsRollupsAppliesABatchOnce(t *testing.T) {
	r, store := newBatchReceiver(t, api.PlanPro)
	accountID, appID := uuid.New(), uuid.New()
	minute := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	row := routeRollupRow(accountID, appID, minute, 3)
	twin := routeRollupRow(accountID, appID, minute, 2)
	twin.DeploymentId = row.DeploymentId
	earlier := routeRollupRow(accountID, appID, minute.Add(-time.Minute), 1)
	req := &apidpb.AddRequestAnalyticsRollupsRequest{BatchId: uuid.NewString(), NodeId: "node-a", Rows: []*apidpb.RequestAnalyticsRollupRow{row, twin, earlier}}
	out, err := r.AddRequestAnalyticsRollups(t.Context(), req)
	if err != nil || out.GetReplayed() || out.GetRejected() != 0 || out.GetNotRetained() != 0 {
		t.Fatalf("first send = %+v, %v", out, err)
	}
	if len(store.rows) != 2 {
		t.Fatalf("rows = %d, want the two minutes with repeated keys merged", len(store.rows))
	}
	var total int64
	for _, r := range store.rows {
		total += r.Requests
	}
	if total != 6 {
		t.Fatalf("requests = %d, want 6", total)
	}
	if len(store.coverage) != 1 || !store.coverage[0].CoveredSince.Equal(minute.Add(-time.Minute)) {
		t.Fatalf("coverage = %+v, want the earlier minute", store.coverage)
	}
	out, err = r.AddRequestAnalyticsRollups(t.Context(), req)
	if err != nil || !out.GetReplayed() || len(store.rows) != 2 {
		t.Fatalf("resend = %+v, %v, rows %d; want a replay that adds nothing", out, err, len(store.rows))
	}
}

func TestAddRequestAnalyticsRollupsLeavesOutPlansWithoutTelemetry(t *testing.T) {
	r, store := newBatchReceiver(t, api.PlanFree)
	minute := time.Now().UTC().Truncate(time.Minute)
	out, err := r.AddRequestAnalyticsRollups(t.Context(), &apidpb.AddRequestAnalyticsRollupsRequest{
		BatchId: uuid.NewString(), Rows: []*apidpb.RequestAnalyticsRollupRow{routeRollupRow(uuid.New(), uuid.New(), minute, 1)},
	})
	if err != nil || out.GetNotRetained() != 1 || len(store.rows) != 0 {
		t.Fatalf("free batch = %+v, %v, rows %d", out, err, len(store.rows))
	}
}

func TestAddRequestAnalyticsRollupsAnswers(t *testing.T) {
	minute := time.Now().UTC().Truncate(time.Minute)
	big := make([]*apidpb.RequestAnalyticsRollupRow, api.RequestAnalyticsRollupBatchRowsMax+1)
	for i := range big {
		big[i] = routeRollupRow(uuid.New(), uuid.New(), minute, 1)
	}
	tests := []struct {
		name  string
		store any
		req   *apidpb.AddRequestAnalyticsRollupsRequest
		want  codes.Code
	}{
		{"store without a ledger", &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}, &apidpb.AddRequestAnalyticsRollupsRequest{BatchId: uuid.NewString()}, codes.Unimplemented},
		{"bad batch id", nil, &apidpb.AddRequestAnalyticsRollupsRequest{BatchId: "x"}, codes.InvalidArgument},
		{"too many rows", nil, &apidpb.AddRequestAnalyticsRollupsRequest{BatchId: uuid.NewString(), Rows: big}, codes.InvalidArgument},
		{"empty probe", nil, &apidpb.AddRequestAnalyticsRollupsRequest{BatchId: uuid.NewString()}, codes.OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := newBatchReceiver(t, api.PlanPro)
			if tt.store != nil {
				r = NewRequestTelemetryReceiver(tt.store.(*consumerTelemetryStore), nil, nil, true)
				NewRequestAnalyticsRollups(&fakeRollupStore{}, nil, nil).applyRequestTelemetry(r)
			}
			_, err := r.AddRequestAnalyticsRollups(t.Context(), tt.req)
			if got := status.Code(err); got != tt.want {
				t.Fatalf("code = %v (%v), want %v", got, err, tt.want)
			}
		})
	}
	t.Run("apid without rollups", func(t *testing.T) {
		store := &rollupBatchTelemetryStore{consumerTelemetryStore: consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}}
		r := NewRequestTelemetryReceiver(store, nil, nil, true)
		_, err := r.AddRequestAnalyticsRollups(t.Context(), &apidpb.AddRequestAnalyticsRollupsRequest{BatchId: uuid.NewString()})
		if status.Code(err) != codes.Unimplemented {
			t.Fatalf("code = %v, want Unimplemented so the gateway leaves counting to apid", status.Code(err))
		}
	})
}

func TestRollupFromProtoRejectsRowsOffContract(t *testing.T) {
	minute := time.Now().UTC().Truncate(time.Minute)
	good := func() *apidpb.RequestAnalyticsRollupRow { return routeRollupRow(uuid.New(), uuid.New(), minute, 2) }
	folded := func() *apidpb.RequestAnalyticsRollupRow {
		p := good()
		p.DimensionKind, p.Dimension, p.Method, p.Status, p.DeploymentId = requestanalytics.KindCountry, "DE", "", requestanalytics.FoldedStatus, uuid.Nil.String()
		return p
	}
	tests := []struct {
		name   string
		mutate func(*apidpb.RequestAnalyticsRollupRow)
		base   func() *apidpb.RequestAnalyticsRollupRow
		ok     bool
	}{
		{"route row", func(*apidpb.RequestAnalyticsRollupRow) {}, good, true},
		{"folded row", func(*apidpb.RequestAnalyticsRollupRow) {}, folded, true},
		{"nil app", func(p *apidpb.RequestAnalyticsRollupRow) { p.AppId = uuid.Nil.String() }, good, false},
		{"unaligned minute", func(p *apidpb.RequestAnalyticsRollupRow) { p.BucketStartUnix += 7 }, good, false},
		{"unknown kind", func(p *apidpb.RequestAnalyticsRollupRow) { p.DimensionKind = "path" }, good, false},
		{"empty dimension", func(p *apidpb.RequestAnalyticsRollupRow) { p.Dimension = "" }, good, false},
		{"invalid utf8", func(p *apidpb.RequestAnalyticsRollupRow) { p.Dimension = "\xff" }, good, false},
		{"route status 0", func(p *apidpb.RequestAnalyticsRollupRow) { p.Status = 0 }, good, false},
		{"folded with method", func(p *apidpb.RequestAnalyticsRollupRow) { p.Method = "GET" }, folded, false},
		{"folded with status", func(p *apidpb.RequestAnalyticsRollupRow) { p.Status = 200 }, folded, false},
		{"no requests", func(p *apidpb.RequestAnalyticsRollupRow) { p.Requests = 0 }, good, false},
		{"errors past requests", func(p *apidpb.RequestAnalyticsRollupRow) { p.ErrorRequests = 3 }, good, false},
		{"short histogram", func(p *apidpb.RequestAnalyticsRollupRow) { p.LatencyHist = p.LatencyHist[:3] }, good, false},
		{"negative bucket", func(p *apidpb.RequestAnalyticsRollupRow) { p.LatencyHist[0] = -1 }, good, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.base()
			tt.mutate(p)
			if _, ok := rollupFromProto(p); ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
		})
	}
}

// A raw row its gateway already counted is stored but not counted again.
func TestRequestAnalyticsRollupsSkipRowsTheGatewayCounted(t *testing.T) {
	rollupStore := &fakeRollupStore{}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
	r := NewRequestTelemetryReceiver(store, nil, nil, true)
	rollups.applyRequestTelemetry(r)
	accountID, appID, deploymentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	counted := rollupTelemetryRequest(accountID, appID, deploymentID, now)
	counted.AnalyticsCounted = true
	r.HandleOne(t.Context(), counted)
	r.HandleOne(t.Context(), rollupTelemetryRequest(accountID, appID, deploymentID, now))
	if err := flushClosed(t, rollups); err != nil {
		t.Fatal(err)
	}
	if got := rollupStore.requests(RollupKindRoute, ""); got != 1 {
		t.Fatalf("route rollup = %d, want only the unflagged row", got)
	}
	if len(store.inserted) != 2 {
		t.Fatalf("raw rows = %d, want both stored", len(store.inserted))
	}
}
