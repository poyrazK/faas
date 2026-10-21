package state_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

// usageByMonthAggregate is UsageByMonth's original query: the sum of every
// usage minute of the month. The trigger-kept totals must match it exactly.
func usageByMonthAggregate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID string, month time.Time) []state.Usage {
	t.Helper()
	rows, err := pool.Query(ctx,
		`select account_id, coalesce(app_id, job_id), $2::timestamptz,
		        sum(mb_seconds)::bigint, sum(cpu_usec)::bigint, sum(requests)::bigint, sum(tx_bytes)::bigint,
		        sum(net_tx_bytes)::bigint, sum(net_rx_bytes)::bigint, sum(cold_boot_count)::bigint
		   from usage_minutes
		  where account_id = $1 and minute >= $2 and minute < $3
		  group by account_id, coalesce(app_id, job_id)
		  order by coalesce(app_id, job_id)`, accountID, month, month.AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	defer rows.Close()
	var out []state.Usage
	for rows.Next() {
		u := state.Usage{}
		if err := rows.Scan(&u.AccountID, &u.AppID, &u.Month, &u.MBSeconds, &u.CPUUsec, &u.Requests, &u.TXBytes, &u.NetTxBytes, &u.NetRxBytes, &u.ColdBootCount); err != nil {
			t.Fatalf("aggregate scan: %v", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("aggregate rows: %v", err)
	}
	return out
}

// TestPgUsageMonthTotalsMatchMinuteAggregate drives the real usage writers
// (first write, additive ON CONFLICT merge, the network delta UPDATE, job
// minutes) and deletes, then checks UsageByMonth against the minute aggregate
// for a tracked month, the month after it and a month before tracking began.
func TestPgUsageMonthTotalsMatchMinuteAggregate(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, appA := seedPgAccountAndApp(t, s, ctx)
	appB, err := s.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: acct.ID, Slug: "s-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	tracked := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	before := tracked.AddDate(0, -1, 0)
	after := tracked.AddDate(0, 1, 0)
	if _, err := pool.Exec(ctx, `update usage_month_totals_tracking set tracked_from = $1`, tracked); err != nil {
		t.Fatalf("set tracked_from: %v", err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	appendApp := func(app state.App, instance string, minute time.Time, mb, req, cpu int64) {
		t.Helper()
		if err := s.AppendUsage(ctx, acct.ID, app.ID, instance, minute, mb, req, cpu, 5, 1, 2, 1, 0); err != nil {
			t.Fatalf("AppendUsage: %v", err)
		}
	}
	inst1, inst2, inst3, inst4 := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	appendApp(appA, inst1, tracked, 100, 2, 10)
	appendApp(appA, inst1, tracked, 999, 3, 20) // merges: counters add, mb_seconds stays
	appendApp(appA, inst2, tracked.Add(time.Hour), 50, 1, 5)
	appendApp(appB, inst3, tracked.Add(2*time.Hour), 70, 4, 7)
	appendApp(appA, inst1, after, 30, 1, 1)
	appendApp(appA, inst4, before, 40, 1, 1) // before tracking: never totalled
	jobID := uuid.NewString()
	if err := s.AppendJobUsage(ctx, acct.ID, jobID, uuid.NewString(), tracked.Add(3*time.Hour), 60, 0, 9, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("AppendJobUsage: %v", err)
	}
	exec(`update usage_minutes set net_tx_bytes = net_tx_bytes + 7, net_rx_bytes = net_rx_bytes + 3 where instance_id = $1 and minute = $2`,
		inst2, tracked.Add(time.Hour))
	exec(`update usage_minutes set requests = requests where instance_id = $1`, inst1) // no-op deltas

	check := func(step string) {
		t.Helper()
		for _, month := range []time.Time{before, tracked, after} {
			got, err := s.UsageByMonth(ctx, acct.ID, month)
			if err != nil {
				t.Fatalf("%s: UsageByMonth(%s): %v", step, month.Format("2006-01"), err)
			}
			if want := usageByMonthAggregate(t, ctx, pool, acct.ID, month); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: UsageByMonth(%s) =\n%+v\nwant the minute aggregate\n%+v", step, month.Format("2006-01"), got, want)
			}
		}
	}
	check("after writes")
	var totalled int
	if err := pool.QueryRow(ctx, `select count(*) from usage_month_totals where account_id = $1 and month = $2`, acct.ID, tracked).Scan(&totalled); err != nil || totalled != 3 {
		t.Fatalf("tracked month totals rows = %d (err %v), want 3 (two apps and a job)", totalled, err)
	}

	// Removing every minute of app B drops it from the tracked month, and a
	// delete before tracking began neither fails nor creates a total.
	exec(`delete from usage_minutes where app_id = $1`, appB.ID)
	exec(`delete from usage_minutes where instance_id = $1`, inst4)
	check("after deletes")
	if got, _ := s.UsageByMonth(ctx, acct.ID, tracked); len(got) != 2 {
		t.Fatalf("tracked month after deleting app B = %+v, want app A and the job", got)
	}
	var untracked int
	if err := pool.QueryRow(ctx, `select count(*) from usage_month_totals where month = $1`, before).Scan(&untracked); err != nil || untracked != 0 {
		t.Fatalf("totals before tracking = %d (err %v), want 0", untracked, err)
	}
}
