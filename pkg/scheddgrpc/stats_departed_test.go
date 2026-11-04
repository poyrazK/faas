package scheddgrpc_test

// adr: 20261103121407

import (
	"context"
	"net"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/sched/instancestats"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func listStatsFrom(t *testing.T, stats scheddgrpc.StatsReader) []*scheddpb.InstanceStatsRow {
	t.Helper()
	srv := grpc.NewServer()
	scheddgrpc.NewWithStats(nil, stats, wire.NewOpsMetrics("schedd_test"), nil).Register(srv)
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
	resp, err := scheddpb.NewScheddClient(conn).ListInstanceStats(context.Background(), &scheddpb.ListInstanceStatsRequest{})
	if err != nil {
		t.Fatalf("ListInstanceStats: %v", err)
	}
	return resp.GetRows()
}

// TestListInstanceStats_ServesDepartedNetworkCounters pins ADR-20261103121407:
// an instance that left the stats snapshot is still listed with its final
// network counters so meterd checkpoints the bytes it sent before it parked.
// Its CPU and disk are not served: the instance is gone.
func TestListInstanceStats_ServesDepartedNetworkCounters(t *testing.T) {
	reader := instancestats.NewReader()
	reader.Replace([]instancestats.InstanceStat{
		{InstanceID: "vm-gone", AppID: "app", NodeID: "node-1", CPUUsageUsec: 77, CPU: instancestats.Valid,
			TXBytes: 5000, TX: instancestats.Valid, RXBytes: 40, RX: instancestats.Valid,
			DiskValid: true, DiskUsedBytes: 1, DiskCapacityBytes: 2},
		{InstanceID: "vm-live", AppID: "app", NodeID: "node-1", TXBytes: 10, TX: instancestats.Valid},
	})
	reader.Replace([]instancestats.InstanceStat{
		{InstanceID: "vm-live", AppID: "app", NodeID: "node-1", TXBytes: 20, TX: instancestats.Valid},
	})

	byID := map[string]*scheddpb.InstanceStatsRow{}
	for _, row := range listStatsFrom(t, reader) {
		byID[row.GetInstanceId()] = row
	}
	if len(byID) != 2 || byID["vm-live"].GetNetTxBytes() != 20 {
		t.Fatalf("rows = %v, want vm-live (20 bytes) and departed vm-gone", byID)
	}
	gone := byID["vm-gone"]
	if gone == nil || gone.GetNetTxBytes() != 5000 || gone.GetTxValid() != 0 || gone.GetNetRxBytes() != 40 || gone.GetRxValid() != 0 {
		t.Fatalf("departed row = %v, want final counters 5000/40, valid", gone)
	}
	if gone.GetCpuValid() != uint32(instancestats.Unknown) || gone.GetDiskUsedBytes() != nil {
		t.Fatalf("departed row serves CPU/disk: cpu_valid=%d disk=%v", gone.GetCpuValid(), gone.GetDiskUsedBytes())
	}
}
