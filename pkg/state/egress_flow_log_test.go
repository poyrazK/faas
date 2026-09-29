// adr: 371 — egress flow log.
package state_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func exerciseEgressFlowLog(t *testing.T, s state.EgressFlowLogStore) {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 9, 24, 19, 0, 0, 0, time.UTC)
	rec := func(minute int, acct, ip string, port uint16) state.EgressFlowRecord {
		return state.EgressFlowRecord{ObservedAt: base.Add(time.Duration(minute) * time.Minute), NodeName: "fsn-2",
			AccountID: acct, AppID: "app-" + acct, InstanceID: "ins-" + acct, RemoteIP: netip.MustParseAddr(ip), RemotePort: port}
	}
	if err := s.InsertEgressFlows(ctx, []state.EgressFlowRecord{
		rec(0, "a1", "198.51.100.10", 443),
		rec(5, "a2", "198.51.100.10", 22),
		rec(10, "a1", "198.51.100.77", 443),
		rec(90, "a1", "203.0.113.1", 443),
	}); err != nil {
		t.Fatalf("InsertEgressFlows: %v", err)
	}
	window := state.EgressFlowFilter{From: base, To: base.Add(time.Hour), Limit: 100}

	byHost := window
	byHost.Remote = netip.PrefixFrom(netip.MustParseAddr("198.51.100.10"), 32)
	got, err := s.ListEgressFlows(ctx, byHost)
	if err != nil || len(got) != 2 || got[0].AccountID != "a2" || got[0].RemotePort != 22 || got[1].AccountID != "a1" {
		t.Fatalf("by host = %+v, %v; want a2:22 then a1:443, newest first", got, err)
	}
	byNet := window
	byNet.Remote = netip.MustParsePrefix("198.51.100.0/24")
	byNet.AccountID = "a1"
	if got, _ := s.ListEgressFlows(ctx, byNet); len(got) != 2 || got[0].RemoteIP.String() != "198.51.100.77" {
		t.Fatalf("by /24 + account = %+v, want two a1 rows, newest first", got)
	}
	limited := window
	limited.Limit = 1
	if got, _ := s.ListEgressFlows(ctx, limited); len(got) != 1 {
		t.Fatalf("limit 1 returned %d rows", len(got))
	}
	if n, err := s.DeleteEgressFlowsBefore(ctx, base.Add(time.Hour), 2); err != nil || n != 2 {
		t.Fatalf("DeleteEgressFlowsBefore = %d, %v; want 2 (batch limit)", n, err)
	}
	all := state.EgressFlowFilter{From: base.Add(-time.Hour), To: base.Add(3 * time.Hour), Limit: 100}
	if got, _ := s.ListEgressFlows(ctx, all); len(got) != 2 {
		t.Fatalf("after one batch %d rows remain, want 2", len(got))
	}
}

func TestMemStoreEgressFlowLog(t *testing.T) {
	exerciseEgressFlowLog(t, state.NewMemStore())
}

func TestPgStoreEgressFlowLog(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM egress_flow_log`); err != nil {
		t.Fatal(err)
	}
	exerciseEgressFlowLog(t, state.NewPgStore(pool))
}
