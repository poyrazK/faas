// End-to-end handler tests via bufconn: an in-process schedd gRPC server backed
// by a fake SchedAPI. Mirrors pkg/vmmdgrpc/bufconn_test.go.

package scheddgrpc_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type fakeEngine struct {
	wakeFn   func(ctx context.Context, appID string) (sched.WakeResult, error)
	reportFn func(ctx context.Context, touches []state.InstanceTouch) (int, error)
	parkFn   func(ctx context.Context, instanceID, reason string) error
}

func (f *fakeEngine) Wake(ctx context.Context, appID string) (sched.WakeResult, error) {
	return f.wakeFn(ctx, appID)
}

func (f *fakeEngine) ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error) {
	return f.reportFn(ctx, touches)
}

func (f *fakeEngine) ParkWithReason(ctx context.Context, instanceID, reason string) error {
	if f.parkFn != nil {
		return f.parkFn(ctx, instanceID, reason)
	}
	return nil
}

func newServer(t *testing.T, eng scheddgrpc.SchedAPI) scheddpb.ScheddClient {
	t.Helper()
	srv := grpc.NewServer()
	scheddgrpc.New(eng, wire.NewOpsMetrics("schedd_test"), nil).Register(srv)

	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return scheddpb.NewScheddClient(conn)
}

func TestWake_Success(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		wakeFn: func(context.Context, string) (sched.WakeResult, error) {
			return sched.WakeResult{InstanceID: "i-1", NodeID: "node-test-1", Method: vmmdpb.WakeMethod_WAKE_RESTORE}, nil
		},
	})
	resp, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if resp.GetNodeId() != "node-test-1" {
		t.Errorf("node_id = %q", resp.GetNodeId())
	}
	if resp.GetMethod() != scheddpb.WakeMethod_WAKE_RESTORE {
		t.Errorf("method = %v, want WAKE_RESTORE", resp.GetMethod())
	}
}

func TestWake_CapacityDenialSurfacesProblem(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		wakeFn: func(context.Context, string) (sched.WakeResult, error) {
			return sched.WakeResult{}, api.ErrCapacity("no RAM headroom")
		},
	})
	_, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{AppId: "app-1"})
	if err == nil {
		t.Fatal("expected capacity denial")
	}
	// api.CodeCapacity maps to ResourceExhausted (grpcerr) so the gateway serves 503.
	if code := status.Code(err); code != codes.ResourceExhausted {
		t.Errorf("code = %v, want ResourceExhausted", code)
	}
}

func TestWake_PlainErrorIsInternal(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		wakeFn: func(context.Context, string) (sched.WakeResult, error) {
			return sched.WakeResult{}, errors.New("db exploded")
		},
	})
	_, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{AppId: "app-1"})
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("code = %v, want Internal", code)
	}
}

func TestReportActivity(t *testing.T) {
	var got []state.InstanceTouch
	cli := newServer(t, &fakeEngine{
		reportFn: func(_ context.Context, touches []state.InstanceTouch) (int, error) {
			got = touches
			return len(touches), nil
		},
	})
	now := time.Now().UnixMilli()
	resp, err := cli.ReportActivity(context.Background(), &scheddpb.ReportActivityRequest{
		Touches: []*scheddpb.Touch{
			{InstanceId: "i-1", UnixMs: now},
			{InstanceId: "i-2", UnixMs: now},
		},
	})
	if err != nil {
		t.Fatalf("ReportActivity: %v", err)
	}
	if resp.GetApplied() != 2 {
		t.Errorf("applied = %d, want 2", resp.GetApplied())
	}
	if len(got) != 2 || got[0].InstanceID != "i-1" {
		t.Errorf("touches = %+v", got)
	}
	if got[0].LastRequest.UnixMilli() != now {
		t.Errorf("touch time round-trip lost: %v", got[0].LastRequest)
	}
}

// TestWake_PropagatesWakeID asserts the per-wake stable identifier
// minted by schedd's engine reaches the gRPC response verbatim. The
// gatewayd client reads resp.GetWakeId() and sets it as the
// x-faas-wake-id response header — if this contract breaks, downstream
// logs and dashboards lose their correlation key.
func TestWake_PropagatesWakeID(t *testing.T) {
	const wantWakeID = "0193f7c0-1234-7abc-9def-0123456789ab"
	cli := newServer(t, &fakeEngine{
		wakeFn: func(context.Context, string) (sched.WakeResult, error) {
			return sched.WakeResult{
				InstanceID: "i-1",
				NodeID:     "node-test-1",
				Method:     vmmdpb.WakeMethod_WAKE_RESTORE,
				WakeID:     wantWakeID,
			}, nil
		},
	})
	resp, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if got := resp.GetWakeId(); got != wantWakeID {
		t.Errorf("wake_id = %q, want %q", got, wantWakeID)
	}
}
