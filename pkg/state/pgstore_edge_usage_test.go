package state_test

// adr: 20261104132753 — bytes Gregale sends for an app without an instance
// (edge frames, realtime fan-out) are metered as the app's egress, and the
// paying account's build seconds this month are read for the build quota.

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func monthNetTx(t *testing.T, s *state.PgStore, acctID, appID string, from, to time.Time) int64 {
	t.Helper()
	ctx := t.Context()
	rows, err := s.UsageByHour(ctx, acctID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, row := range rows {
		if row.AppID == appID {
			total += row.NetTxBytes
		}
	}
	return total
}

func TestPg_GatewayEdgeUsageEventIsAppEgressExactlyOnce(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)
	minute := time.Date(2026, 11, 4, 13, 30, 0, 0, time.UTC)
	eventID := uuid.NewString()
	for range 2 { // a replay before the ACK stays exactly once
		if err := s.AppendGatewayEdgeUsageEvent(ctx, nodeID, eventID, appID, minute, 5<<20); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendGatewayEdgeUsageEvent(ctx, nodeID, uuid.NewString(), appID, minute, 1<<20); err != nil {
		t.Fatal(err)
	}
	if got := monthNetTx(t, s, acctID, appID, minute.Add(-time.Hour), minute.Add(time.Hour)); got != 6<<20 {
		t.Fatalf("edge egress = %d, want %d", got, 6<<20)
	}
	// A frame for an app that no longer exists is recorded and dropped.
	if err := s.AppendGatewayEdgeUsageEvent(ctx, nodeID, uuid.NewString(), uuid.NewString(), minute, 1); err != nil {
		t.Fatalf("unknown app: %v, want nil so the frame is acknowledged", err)
	}
}

func TestPg_SyncRealtimeEgressUsageAppendsOnlyNewBytes(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)
	now := time.Date(2026, 11, 4, 13, 30, 0, 0, time.UTC)
	endpoint := uuid.NewString()
	report := func(hour time.Time, bytes int64) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO realtime_usage_hours (endpoint_id, hour, node_name, incarnation, account_id, app_id,
			minute_peak_connections, connection_seconds, delivered_messages, delivered_bytes, received_messages, received_bytes)
			VALUES ($1, $2, 'rt-1', '00000000-0000-0000-0000-000000000001', $3, $4, array_fill(0, ARRAY[60]), 0, 1, $5, 0, 0)
			ON CONFLICT (endpoint_id, hour, node_name, incarnation) DO UPDATE SET delivered_bytes = greatest(realtime_usage_hours.delivered_bytes, excluded.delivered_bytes)`,
			endpoint, hour, acctID, appID, bytes)
		if err != nil {
			t.Fatal(err)
		}
	}
	report(time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC), 1<<30) // last month: not counted
	report(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC), 3<<20)
	if _, err := s.SyncRealtimeEgressUsage(ctx, now); err != nil {
		t.Fatal(err)
	}
	report(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC), 4<<20) // the hour's count grew
	for range 2 {                                                // a second sync with nothing new appends nothing
		if _, err := s.SyncRealtimeEgressUsage(ctx, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if got := monthNetTx(t, s, acctID, appID, from, from.AddDate(0, 1, 0)); got != 4<<20 {
		t.Fatalf("realtime egress this month = %d, want %d", got, 4<<20)
	}
}

func TestPg_BuildSecondsThisMonthCountsThePayersBuilds(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	build := func(started time.Time, seconds int, coalesced bool) {
		t.Helper()
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.CreateBuild(ctx, dep.ID, state.DeploymentKindTarball, 1024, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE builds SET started_at = $2, finished_at = $3 WHERE id = $1`,
			b.ID, started, started.Add(time.Duration(seconds)*time.Second)); err != nil {
			t.Fatal(err)
		}
		if coalesced {
			if _, err := pool.Exec(ctx, `UPDATE builds SET coalesced_with = id WHERE id = $1`, b.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	build(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), 900, false) // last month
	build(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC), 120, false)
	build(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), 300, false)
	build(time.Date(2026, 11, 3, 13, 0, 0, 0, time.UTC), 600, true) // coalesced: no builder of its own
	plan, seconds, err := s.BuildSecondsThisMonth(ctx, appID, now)
	if err != nil {
		t.Fatal(err)
	}
	if plan != api.PlanPro || seconds != 420 {
		t.Fatalf("plan=%q seconds=%d, want pro and 420", plan, seconds)
	}
}
