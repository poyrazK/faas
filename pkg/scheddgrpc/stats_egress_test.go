package scheddgrpc_test

// Wire-shape tests for ADR-046 (step 7): the schedd
// ListInstanceStats RPC surfaces per-instance egress bytes
// via the InstanceStatsRow.net_tx_bytes / tx_valid fields
// (after the CPU-µs fields). The handler reads from
// instancestats.Reader (the SchedAPI-level poller), and the
// tests below pin:
//
//   - empty Reader → empty Rows (no panic, no error)
//   - Reader row with TX=Valid, TXBytes=4096 → wire row
//     carries the same values
//   - Reader row with TX=Unknown → wire tx_valid=1 (and
//     net_tx_bytes is whatever the row holds, 0 here — the
//     sampler skips Unknown rows so the value is irrelevant)
//
// These mirror the cpu_usec round-trip tests for issue #279 /
// PR-B / ADR-039; the only difference is the new fields.

import (
	"context"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/sched/instancestats"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"net"
)

// fakeStatsReader is a programmatic instancestats.Reader. The
// tests below construct one directly with a known row set and
// hand it to scheddgrpc.NewWithStats. There is no production
// equivalent — the wire-shape tests only need to prove the
// handler reads from the reader and stamps the proto fields.
type fakeStatsReader struct {
	rows []instancestats.InstanceStat
}

func (f *fakeStatsReader) SnapshotAll() []instancestats.InstanceStat {
	return f.rows
}

// newServerWithStats wires a scheddgrpc.Server with a stats
// reader for the ListInstanceStats handler. Mirrors
// newServer in bufconn_test.go but uses NewWithStats so the
// wire-shape tests below can drive the reader.
func newServerWithStats(t *testing.T, stats *fakeStatsReader) scheddpb.ScheddClient {
	t.Helper()
	srv := grpc.NewServer()
	// The SchedAPI engine can be nil for these wire-shape
	// tests — ListInstanceStats does not touch it.
	scheddgrpc.NewWithStats(nil, stats, wire.NewOpsMetrics("schedd_test"), nil).Register(srv)

	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return scheddpb.NewScheddClient(conn)
}

// TestListInstanceStats_EmptyReader_ReturnsEmptyRows pins
// the no-panic / no-error contract: a Stats reader with no
// rows produces an empty response. This is the PR-1 fallback
// — meterd's sampler fold-in (PR-2) will land against an empty
// reader and degrade to "no egress data" without restart.
func TestListInstanceStats_EmptyReader_ReturnsEmptyRows(t *testing.T) {
	stats := &fakeStatsReader{}
	cli := newServerWithStats(t, stats)

	resp, err := cli.ListInstanceStats(context.Background(), &scheddpb.ListInstanceStatsRequest{})
	if err != nil {
		t.Fatalf("ListInstanceStats: %v", err)
	}
	if got := len(resp.GetRows()); got != 0 {
		t.Errorf("rows = %d, want 0 (empty reader)", got)
	}
}

// TestListInstanceStats_NetTxBytesPopulatedWhenValid pins
// the happy path: a row stamped TX=Valid with TXBytes=4096
// surfaces on the wire as net_tx_bytes=4096 and
// tx_valid=0 (instancestats.Valid). The meterd sampler
// (PR-2) reads net_tx_bytes and tx_valid together to know
// the delta is fresh.
func TestListInstanceStats_NetTxBytesPopulatedWhenValid(t *testing.T) {
	stats := &fakeStatsReader{rows: []instancestats.InstanceStat{
		{
			InstanceID: "vm-A",
			AppID:      "app-1",
			NodeID:     "node-1",
			TXBytes:    4096,
			TX:         instancestats.Valid,
			RXBytes:    2048,
			RX:         instancestats.Valid,
		},
	}}
	cli := newServerWithStats(t, stats)

	resp, err := cli.ListInstanceStats(context.Background(), &scheddpb.ListInstanceStatsRequest{})
	if err != nil {
		t.Fatalf("ListInstanceStats: %v", err)
	}
	rows := resp.GetRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].GetNetTxBytes() != 4096 {
		t.Errorf("net_tx_bytes = %d, want 4096", rows[0].GetNetTxBytes())
	}
	// Wire contract (ADR-046 PR-414 I8): tx_valid is the
	// instancestats.Validity enum literal cast to uint32.
	// 0 = Valid, 1 = Unknown (matches the cpu_valid wire
	// convention). Pin the literal so a future enum reorder
	// in pkg/sched/instancestats breaks this test, not the
	// wire.
	if rows[0].GetTxValid() != uint32(0) {
		t.Errorf("tx_valid = %d, want 0 (Valid)", rows[0].GetTxValid())
	}
	if rows[0].GetNetRxBytes() != 2048 || rows[0].GetRxValid() != uint32(0) {
		t.Errorf("ingress = (%d, valid=%d), want (2048, 0)", rows[0].GetNetRxBytes(), rows[0].GetRxValid())
	}
}

// TestListInstanceStats_TxUnknown_RoundTripsUnknown pins the
// Unknown-validity branch: a row with TX=Unknown surfaces as
// tx_valid=1. The meterd sampler (PR-2) skips rows where
// tx_valid != 0 — the wire-shape test only needs to prove the
// value travels, not the sampler semantics.
func TestListInstanceStats_TxUnknown_RoundTripsUnknown(t *testing.T) {
	stats := &fakeStatsReader{rows: []instancestats.InstanceStat{
		{
			InstanceID: "vm-A",
			AppID:      "app-1",
			NodeID:     "node-1",
			TXBytes:    0,
			TX:         instancestats.Unknown,
		},
	}}
	cli := newServerWithStats(t, stats)

	resp, err := cli.ListInstanceStats(context.Background(), &scheddpb.ListInstanceStatsRequest{})
	if err != nil {
		t.Fatalf("ListInstanceStats: %v", err)
	}
	rows := resp.GetRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	// Wire contract (ADR-046 PR-414 I8): see
	// TestListInstanceStats_TxValid_RoundTripsValid — 1 =
	// Unknown on the wire.
	if rows[0].GetTxValid() != uint32(1) {
		t.Errorf("tx_valid = %d, want 1 (Unknown)", rows[0].GetTxValid())
	}
}
