package scheddgrpc_test

import (
	"context"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type pressureEngine struct {
	*fakeEngine
	calls []int
	fn    func() (bool, string, error)
}

func (e *pressureEngine) QueueMemoryPressureCapture(_ context.Context, _ string, availableMB, totalMB, anonMB int) (bool, string, error) {
	e.calls = append(e.calls, availableMB, totalMB, anonMB)
	return e.fn()
}

func TestReportMemoryPressure(t *testing.T) {
	eng := &pressureEngine{fakeEngine: &fakeEngine{}, fn: func() (bool, string, error) { return true, "", nil }}
	srv := scheddgrpc.New(eng, wire.NewOpsMetrics("schedd_pressure_test"), nil)
	req := &scheddpb.ReportMemoryPressureRequest{InstanceId: "ins-1", AvailableMb: 30, TotalMb: 246, AnonMb: 190}
	ack, err := srv.ReportMemoryPressure(t.Context(), req)
	if err != nil || !ack.GetQueued() || len(eng.calls) != 3 || eng.calls[0] != 30 || eng.calls[2] != 190 {
		t.Fatalf("ack=%v err=%v calls=%v", ack, err, eng.calls)
	}
	eng.fn = func() (bool, string, error) { return false, "rate limited", nil }
	if ack, err := srv.ReportMemoryPressure(t.Context(), req); err != nil || ack.GetQueued() || ack.GetReason() != "rate limited" {
		t.Fatalf("skipped capture: ack=%v err=%v", ack, err)
	}
	eng.fn = func() (bool, string, error) { return false, "", state.ErrNotFound }
	if _, err := srv.ReportMemoryPressure(t.Context(), req); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown instance: %v", err)
	}
	if _, err := srv.ReportMemoryPressure(t.Context(), &scheddpb.ReportMemoryPressureRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing instance: %v", err)
	}
}
