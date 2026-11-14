package receivers

// adr: 20261108160000 — slice B: in-guest OTLP metric exports reach the
// custom metric sink after plan admission, their own rate cap and decoding.

import (
	"context"
	"errors"
	"testing"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
)

type fakeGuestMetricsSink struct {
	calls   int
	account string
	app     string
	metrics int
	err     error
}

func (f *fakeGuestMetricsSink) StoreGuestMetrics(_ context.Context, accountID, appID string, export *colmetricspb.ExportMetricsServiceRequest) (int64, int64, error) {
	f.calls++
	f.account, f.app = accountID, appID
	f.metrics = 0
	for _, rm := range export.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			f.metrics += len(sm.GetMetrics())
		}
	}
	if f.err != nil {
		return 0, 0, f.err
	}
	return int64(f.metrics), 1, nil
}

func guestMetricsExport() *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{ScopeMetrics: []*metricspb.ScopeMetrics{{
		Metrics: []*metricspb.Metric{{Name: "orders.pending", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
			DataPoints: []*metricspb.NumberDataPoint{{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 3}}},
		}}}},
	}}}}}
}

func newGuestMetricsTest(t *testing.T) (*GuestSpansIngester, *fakeGuestMetricsSink, uuid.UUID, uuid.UUID) {
	t.Helper()
	pro, free := uuid.New(), uuid.New()
	accounts := &fakeGuestSpansAccounts{plans: map[string]api.Plan{pro.String(): api.PlanPro, free.String(): api.PlanFree}}
	sink := &fakeGuestMetricsSink{}
	return NewGuestSpansIngester(accounts, peraccount.NewLimiter(), discardLogger()).WithMetrics(sink), sink, pro, free
}

func TestGuestMetricsReachTheSink(t *testing.T) {
	g, sink, pro, _ := newGuestMetricsTest(t)
	ctx := context.Background()
	body, err := proto.Marshal(guestMetricsExport())
	if err != nil {
		t.Fatal(err)
	}
	req := guestSpansRequest(pro, api.TraceCodecProtobuf, body)
	resp := g.ingestMetrics(ctx, req)
	if resp.GetOutcome() != guestSpansAccepted || resp.GetAcceptedPoints() != 1 || resp.GetRejectedPoints() != 1 {
		t.Fatalf("response = %+v", resp)
	}
	if sink.account != pro.String() || sink.app != req.GetAppId() || sink.metrics != 1 {
		t.Fatalf("sink saw account=%q app=%q metrics=%d", sink.account, sink.app, sink.metrics)
	}

	// OTLP/JSON metrics carry no IDs, so they are accepted.
	jsonBody, err := protojson.Marshal(guestMetricsExport())
	if err != nil {
		t.Fatal(err)
	}
	if got := g.ingestMetrics(ctx, guestSpansRequest(pro, api.TraceCodecJSON, jsonBody)).GetOutcome(); got != guestSpansAccepted {
		t.Fatalf("JSON export = %q", got)
	}
}

func TestGuestMetricsOutcomes(t *testing.T) {
	g, sink, pro, free := newGuestMetricsTest(t)
	ctx := context.Background()
	body, err := proto.Marshal(guestMetricsExport())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		acct uuid.UUID
		body []byte
		want string
	}{
		{name: "plan without custom metrics", acct: free, body: body, want: guestSpansDisabled},
		{name: "garbage", acct: pro, body: []byte{0xff, 0xff}, want: guestSpansInvalid},
		{name: "empty", acct: pro, body: nil, want: guestSpansInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink.calls = 0
			if got := g.ingestMetrics(ctx, guestSpansRequest(tc.acct, api.TraceCodecProtobuf, tc.body)).GetOutcome(); got != tc.want || sink.calls != 0 {
				t.Fatalf("outcome = %q (sink calls %d), want %q and no store", got, sink.calls, tc.want)
			}
		})
	}

	sink.err = ErrGuestMetricsDisabled
	if got := g.ingestMetrics(ctx, guestSpansRequest(pro, api.TraceCodecProtobuf, body)).GetOutcome(); got != guestSpansDisabled {
		t.Fatalf("sink disabled = %q", got)
	}
	sink.err = errors.New("db down")
	if got := g.ingestMetrics(ctx, guestSpansRequest(pro, api.TraceCodecProtobuf, body)).GetOutcome(); got != guestSpansUnavailable {
		t.Fatalf("sink failure = %q", got)
	}
	sink.err = nil

	limited := 0
	for i := 0; i < api.MustLimitsFor(api.PlanPro).DebugGuestLogExportsPerMinute+5; i++ {
		if g.ingestMetrics(ctx, guestSpansRequest(pro, api.TraceCodecProtobuf, body)).GetOutcome() == guestSpansRateLimited {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("metric exports were never rate limited")
	}
}

func TestGuestMetricsUnavailableWithoutWithMetrics(t *testing.T) {
	g := NewGuestSpansIngester(&fakeGuestSpansAccounts{}, peraccount.NewLimiter(), discardLogger())
	if got := g.ingestMetrics(context.Background(), guestSpansRequest(uuid.New(), api.TraceCodecProtobuf, []byte("x"))).GetOutcome(); got != guestSpansUnavailable {
		t.Fatalf("outcome = %q", got)
	}
}
