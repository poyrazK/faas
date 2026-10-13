package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreExecutionComputeUsageIsBilledOnce(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	account := executionTestAccount(t, store, "billing")
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)

	params := executionTestParams(t, account.ID, base, 0, "sealed")
	params.Request.Limits.MemoryMB = 256
	created, err := store.CreateExecution(ctx, params)
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "schedd-a", base.Add(time.Millisecond), time.Minute)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	startedAt := base.Add(100 * time.Millisecond)
	if _, err := store.MarkExecutionRunning(ctx, created.ID, *claim.LeaseToken, startedAt); err != nil {
		t.Fatalf("MarkExecutionRunning: %v", err)
	}
	if _, err := store.CompleteExecution(ctx, CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: []byte(`1`), Usage: api.ExecutionUsage{WallTimeMS: 2400, CPUTimeMS: 7},
		FinishedAt: startedAt.Add(2400 * time.Millisecond),
	}); err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	// A repeated terminal acknowledgement must not add a second usage row.
	if _, err := store.RequestExecutionCancellation(ctx, account.ID, created.ID, startedAt.Add(3*time.Second)); err != nil {
		t.Fatalf("RequestExecutionCancellation on terminal run: %v", err)
	}

	const wantMBSeconds = (256 + api.PerVMOverheadMB) * 3 // 2.4 s rounds up to 3 s
	var runRows int
	for _, row := range store.usage {
		if row.InstanceID != created.ID {
			continue
		}
		runRows++
		if row.MeterKind != "run" || row.AppID != account.ID || row.MBSeconds != wantMBSeconds || row.CPUUsec != 7000 {
			t.Fatalf("run usage row = %+v, want run/%d MB-s", row, wantMBSeconds)
		}
	}
	if runRows != 1 {
		t.Fatalf("run usage rows = %d, want 1", runRows)
	}
	monthly, err := store.UsageByMonth(ctx, account.ID, base)
	if err != nil {
		t.Fatalf("UsageByMonth: %v", err)
	}
	if len(monthly) != 1 || monthly[0].MeterKind != "run" || monthly[0].MBSeconds != wantMBSeconds {
		t.Fatalf("monthly usage = %+v, want one run row of %d MB-s", monthly, wantMBSeconds)
	}
}

func TestMemStoreExecutionWithoutRunningIsNotBilled(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	account := executionTestAccount(t, store, "billing-restore-failed")
	base := time.Now().UTC().Add(-time.Hour)
	created, err := store.CreateExecution(ctx, executionTestParams(t, account.ID, base, 0, "sealed"))
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "schedd-a", base.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	code, message := "restore_failed", "execution environment could not be prepared"
	if _, err := store.CompleteExecution(ctx, CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusFailed,
		FailureCode: &code, FailureMessage: &message, FinishedAt: base.Add(time.Second),
	}); err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	for _, row := range store.usage {
		if row.InstanceID == created.ID {
			t.Fatalf("restore failure was billed: %+v", row)
		}
	}
}

func TestExecutionBilledSecondsRoundsUp(t *testing.T) {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		elapsed time.Duration
		want    int64
	}{
		{0, 1},
		{time.Millisecond, 1},
		{time.Second, 1},
		{time.Second + time.Nanosecond, 2},
		{30 * time.Second, 30},
	} {
		if got := executionBilledSeconds(start, start.Add(tc.elapsed)); got != tc.want {
			t.Errorf("executionBilledSeconds(%v) = %d, want %d", tc.elapsed, got, tc.want)
		}
	}
}
