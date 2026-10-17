package receivers

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeUsageNodes struct{}

func (fakeUsageNodes) ComputeNodeByName(_ context.Context, name string) (state.ComputeNode, error) {
	if name != "node-a" {
		return state.ComputeNode{}, state.ErrNotFound
	}
	return state.ComputeNode{ID: "n1", Name: name}, nil
}

type fakeUsageStore struct{ reports []state.RealtimeUsageReport }

func (f *fakeUsageStore) RecordRealtimeUsage(_ context.Context, r state.RealtimeUsageReport) (int, error) {
	if err := state.ValidateRealtimeUsageReport(r, time.Now()); err != nil {
		return 0, err
	}
	f.reports = append(f.reports, r)
	return len(r.Endpoints), nil
}

func (f *fakeUsageStore) ReadRealtimeUsage(context.Context, string, time.Time, time.Time) ([]state.RealtimeUsageHour, error) {
	return nil, nil
}

func newUsageTestClient(t *testing.T, receiver *realtimeFleetReceiver, nodeName string) *apidgrpc.RealtimeHistoryClientImpl {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	server.RegisterService(&realtimeFleetServiceDesc, receiver)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return apidgrpc.NewRealtimeHistoryClientForNode(conn, nodeName)
}

func usageTestReport() state.RealtimeUsageReport {
	return state.RealtimeUsageReport{
		NodeName: "spoofed", Incarnation: uuid.NewString(), Hour: time.Now().UTC().Truncate(time.Hour),
		Endpoints: []state.RealtimeEndpointUsage{{EndpointID: uuid.NewString(), MinutePeakConnections: make([]int32, state.RealtimeUsageMinutes), ConnectionSeconds: 9}},
	}
}

func TestReportUsageRPC(t *testing.T) {
	store := &fakeUsageStore{}
	client := newUsageTestClient(t, &realtimeFleetReceiver{nodes: fakeUsageNodes{}, usage: store}, "node-a")
	if err := client.ReportRealtimeUsage(t.Context(), usageTestReport()); err != nil {
		t.Fatalf("ReportRealtimeUsage: %v", err)
	}
	if len(store.reports) != 1 || store.reports[0].NodeName != "node-a" || store.reports[0].Endpoints[0].ConnectionSeconds != 9 {
		t.Fatalf("recorded = %+v, want the client's node name, not the caller's", store.reports)
	}

	invalid := usageTestReport()
	invalid.Hour = invalid.Hour.Add(time.Minute)
	if err := client.ReportRealtimeUsage(t.Context(), invalid); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unaligned hour err = %v, want InvalidArgument", err)
	}
	unknown := newUsageTestClient(t, &realtimeFleetReceiver{nodes: fakeUsageNodes{}, usage: store}, "node-x")
	if err := unknown.ReportRealtimeUsage(t.Context(), usageTestReport()); status.Code(err) != codes.NotFound {
		t.Fatalf("unregistered node err = %v, want NotFound", err)
	}
	disabled := newUsageTestClient(t, &realtimeFleetReceiver{nodes: fakeUsageNodes{}}, "node-a")
	if err := disabled.ReportRealtimeUsage(t.Context(), usageTestReport()); status.Code(err) != codes.Unavailable {
		t.Fatalf("no usage store err = %v, want Unavailable", err)
	}
	if len(store.reports) != 1 {
		t.Fatalf("rejected reports were recorded: %+v", store.reports)
	}
}
