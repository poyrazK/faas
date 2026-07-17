package scheddgrpc_test

import (
	"context"
	"net"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// newClient stands up an in-process schedd server backed by eng and returns a
// scheddgrpc.Client dialed to it (the same wrapper gatewayd uses).
func newClient(t *testing.T, eng scheddgrpc.SchedAPI) *scheddgrpc.Client {
	t.Helper()
	srv := grpc.NewServer()
	scheddgrpc.New(eng, wire.NewOpsMetrics("schedd_client_test"), nil).Register(srv)

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
	c := scheddgrpc.NewClient(conn)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientWake_ReturnsAddr(t *testing.T) {
	c := newClient(t, &fakeEngine{
		wakeFn: func(_ context.Context, appID string) (sched.WakeResult, error) {
			if appID != "app-1" {
				t.Errorf("appID = %q", appID)
			}
			return sched.WakeResult{InstanceID: "i-1", Addr: "10.100.0.2:8080", Method: vmmdpb.WakeMethod_WAKE_RESTORE}, nil
		},
	})
	instanceID, addr, err := c.Wake(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if addr != "10.100.0.2:8080" {
		t.Errorf("addr = %q", addr)
	}
	if instanceID != "i-1" {
		t.Errorf("instanceID = %q, want i-1", instanceID)
	}
}

func TestClientWake_CapacityLiftsToProblem(t *testing.T) {
	c := newClient(t, &fakeEngine{
		wakeFn: func(context.Context, string) (sched.WakeResult, error) {
			return sched.WakeResult{}, api.ErrCapacity("no RAM headroom")
		},
	})
	_, _, err := c.Wake(context.Background(), "app-1")
	if err == nil {
		t.Fatal("expected capacity denial")
	}
	// The wire status must lift back to *api.Problem so the gateway maps it to
	// the right RFC 7807 response (503) without re-classifying strings.
	prob := api.AsProblem(err)
	if prob == nil {
		t.Fatalf("error did not lift to *api.Problem: %v", err)
	}
	if prob.Status != 503 {
		t.Errorf("problem status = %d, want 503", prob.Status)
	}
}

func TestClientReportActivity(t *testing.T) {
	var got []state.InstanceTouch
	c := newClient(t, &fakeEngine{
		reportFn: func(_ context.Context, touches []state.InstanceTouch) (int, error) {
			got = touches
			return len(touches), nil
		},
	})
	now := time.UnixMilli(1_700_000_000_000)
	applied, err := c.ReportActivity(context.Background(), []state.InstanceTouch{
		{InstanceID: "i-1", LastRequest: now},
		{InstanceID: "i-2", LastRequest: now},
	})
	if err != nil {
		t.Fatalf("ReportActivity: %v", err)
	}
	if applied != 2 {
		t.Errorf("applied = %d, want 2", applied)
	}
	if len(got) != 2 || got[0].InstanceID != "i-1" || !got[0].LastRequest.Equal(now) {
		t.Errorf("touches round-trip = %+v", got)
	}
}

func TestDial_EmptyPath(t *testing.T) {
	if _, err := scheddgrpc.Dial(""); err == nil {
		t.Fatal("expected error on empty socket path")
	}
}

func TestClient_CloseNilConn(t *testing.T) {
	var c scheddgrpc.Client
	if err := c.Close(); err != nil {
		t.Errorf("Close on zero client = %v, want nil", err)
	}
}
