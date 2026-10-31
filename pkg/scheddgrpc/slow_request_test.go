package scheddgrpc_test

import (
	"context"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type slowRequestEngine struct {
	*fakeEngine
	got []string
	in  time.Duration
	fn  func() (bool, string, error)
}

func (e *slowRequestEngine) QueueSlowRequestCapture(_ context.Context, instanceID, requestID, traceID, route string, inflight time.Duration) (bool, string, error) {
	e.got, e.in = []string{instanceID, requestID, traceID, route}, inflight
	return e.fn()
}

func TestReportSlowRequest(t *testing.T) {
	eng := &slowRequestEngine{fakeEngine: &fakeEngine{}, fn: func() (bool, string, error) { return true, "", nil }}
	srv := scheddgrpc.New(eng, wire.NewOpsMetrics("schedd_slow_request_test"), nil)
	req := &scheddpb.ReportSlowRequestRequest{InstanceId: "ins-1", RequestId: "req-1", TraceId: "trace-1", Route: "GET /orders/{id}", InflightMs: 12_500}
	ack, err := srv.ReportSlowRequest(t.Context(), req)
	if err != nil || !ack.GetQueued() || len(eng.got) != 4 || eng.got[1] != "req-1" || eng.got[2] != "trace-1" || eng.got[3] != "GET /orders/{id}" || eng.in != 12500*time.Millisecond {
		t.Fatalf("ack=%v err=%v got=%v inflight=%s", ack, err, eng.got, eng.in)
	}
	eng.fn = func() (bool, string, error) { return false, "rate limited", nil }
	if ack, err := srv.ReportSlowRequest(t.Context(), req); err != nil || ack.GetQueued() || ack.GetReason() != "rate limited" {
		t.Fatalf("skipped capture: ack=%v err=%v", ack, err)
	}
	eng.fn = func() (bool, string, error) { return false, "", state.ErrNotFound }
	if _, err := srv.ReportSlowRequest(t.Context(), req); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown instance: %v", err)
	}
	if _, err := srv.ReportSlowRequest(t.Context(), &scheddpb.ReportSlowRequestRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing instance: %v", err)
	}
	plain := scheddgrpc.New(&fakeEngine{}, wire.NewOpsMetrics("schedd_slow_request_plain_test"), nil)
	if ack, err := plain.ReportSlowRequest(t.Context(), req); err != nil || ack.GetQueued() || ack.GetReason() == "" {
		t.Fatalf("engine without automatic captures: ack=%v err=%v", ack, err)
	}
}
