//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

type pgRollupExecer struct{ pool *pgxpool.Pool }

func (e pgRollupExecer) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := e.pool.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}

func TestPgStoreExecutionComputeUsageFeedsBilling(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "billing")
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)

	params := pgExecutionParams(t, account.ID, base, 0, "sealed-billing")
	params.Request.Limits.MemoryMB = 256
	created, err := store.CreateExecution(ctx, params)
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "schedd-billing", base.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	startedAt := base.Add(100 * time.Millisecond)
	if _, err := store.MarkExecutionRunning(ctx, created.ID, *claim.LeaseToken, startedAt); err != nil {
		t.Fatalf("MarkExecutionRunning: %v", err)
	}
	if _, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: []byte(`1`), Usage: api.ExecutionUsage{WallTimeMS: 2400, CPUTimeMS: 7},
		FinishedAt: startedAt.Add(2400 * time.Millisecond),
	}); err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	// Re-acknowledging a terminal run re-runs usage recording; it must be idempotent.
	if _, err := store.RequestExecutionCancellation(ctx, account.ID, created.ID, startedAt.Add(3*time.Second)); err != nil {
		t.Fatalf("RequestExecutionCancellation on terminal run: %v", err)
	}

	const wantMBSeconds = int64(256+api.PerVMOverheadMB) * 3 // 2.4 s rounds up to 3 s
	var rows int
	var mbSeconds, cpuUsec int64
	var meterKind string
	if err := pool.QueryRow(ctx, `select count(*), coalesce(sum(mb_seconds), 0), coalesce(sum(cpu_usec), 0), coalesce(max(meter_kind), '')
		from usage_minutes where execution_id = $1::uuid`, created.ID).Scan(&rows, &mbSeconds, &cpuUsec, &meterKind); err != nil {
		t.Fatalf("read run usage: %v", err)
	}
	if rows != 1 || mbSeconds != wantMBSeconds || cpuUsec != 7000 || meterKind != "run" {
		t.Fatalf("run usage rows=%d mb_seconds=%d cpu_usec=%d kind=%q; want 1/%d/7000/run", rows, mbSeconds, cpuUsec, meterKind, wantMBSeconds)
	}

	monthly, err := store.UsageByMonth(ctx, account.ID, base)
	if err != nil {
		t.Fatalf("UsageByMonth: %v", err)
	}
	if len(monthly) != 1 || monthly[0].AppID != account.ID || monthly[0].MeterKind != "run" || monthly[0].MBSeconds != wantMBSeconds {
		t.Fatalf("monthly usage = %+v, want one run row keyed by the account", monthly)
	}
	hourly, err := store.UsageByHour(ctx, account.ID, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("UsageByHour: %v", err)
	}
	if len(hourly) != 1 || hourly[0].MBSeconds != wantMBSeconds {
		t.Fatalf("hourly usage = %+v, want the run in the billing window", hourly)
	}

	var attribution []byte
	if err := pool.QueryRow(ctx, `select attribution from financial_usage_evidence
		where instance_id = $1::uuid and meter = 'compute'`, created.ID).Scan(&attribution); err != nil {
		t.Fatalf("read financial evidence: %v", err)
	}
	var attributed map[string]string
	if err := json.Unmarshal(attribution, &attributed); err != nil || attributed["name"] != "runs" {
		t.Fatalf("financial attribution = %s, want name runs", attribution)
	}

	day := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, time.UTC)
	if _, err := meter.RollupOnce(ctx, pgRollupExecer{pool: pool}, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatalf("RollupOnce with a run row: %v", err)
	}
	daily, err := store.UsageDaily(ctx, account.ID, base)
	if err != nil {
		t.Fatalf("UsageDaily: %v", err)
	}
	if len(daily) != 1 || daily[0].MeterKind != "run" || daily[0].AppID != account.ID || daily[0].MBSeconds != wantMBSeconds {
		t.Fatalf("daily usage = %+v, want one run row keyed by the account", daily)
	}
}

func TestPgStoreExecutionWithoutRunningIsNotBilled(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "billing-restore-failed")
	base := time.Now().UTC().Add(-time.Hour)
	created, err := store.CreateExecution(ctx, pgExecutionParams(t, account.ID, base, 0, "sealed-billing"))
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "schedd-billing", base.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	code, message := "restore_failed", "execution environment could not be prepared"
	if _, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusFailed,
		FailureCode: &code, FailureMessage: &message, FinishedAt: base.Add(time.Second),
	}); err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `select count(*) from usage_minutes where execution_id = $1::uuid`, created.ID).Scan(&rows); err != nil {
		t.Fatalf("count run usage: %v", err)
	}
	if rows != 0 {
		t.Fatalf("restore failure produced %d usage rows, want 0", rows)
	}
}
