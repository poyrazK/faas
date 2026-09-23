// adr: 213

package meter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

type deadlinePartitionDB struct {
	deadline time.Time
}

func (db *deadlinePartitionDB) Exec(ctx context.Context, _ string, _ ...any) (int64, error) {
	var ok bool
	db.deadline, ok = ctx.Deadline()
	if !ok {
		return 0, errors.New("partition reconciliation has no deadline")
	}
	return 0, context.DeadlineExceeded
}

func (*deadlinePartitionDB) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("coverage query must not run after failed reconciliation")
}

func TestEnsureLogEventPartitionsBoundsExclusiveLockWork(t *testing.T) {
	db := &deadlinePartitionDB{}
	started := time.Now()
	_, err := EnsureLogEventPartitions(context.Background(), db)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reconciliation error = %v, want deadline exceeded", err)
	}
	if db.deadline.Before(started) || db.deadline.After(started.Add(LogEventPartitionReconcileTimeout+time.Second)) {
		t.Fatalf("reconciliation deadline %s is outside the bounded work window", db.deadline)
	}
}

func TestRetentionOnceLogEventsUsesBoundedBatches(t *testing.T) {
	db := &recordingExecer{rowsFn: func(i int) int64 {
		if i == 0 {
			return RetentionBatchSize
		}
		return 7
	}}
	deleted, err := RetentionOnceLogEvents(context.Background(), db)
	if err != nil || deleted != RetentionBatchSize+7 {
		t.Fatalf("deleted=%d, err=%v", deleted, err)
	}
	calls := db.callsCopy()
	if len(calls) != 2 {
		t.Fatalf("calls=%d, want 2", len(calls))
	}
	for _, call := range calls {
		if len(call.Args) != 1 || call.Args[0] != RetentionBatchSize {
			t.Fatalf("unexpected batch arguments: %+v", call.Args)
		}
	}
}

func TestLogEventRetentionWindowsMatchPlanLimits(t *testing.T) {
	for _, tc := range []struct {
		plan api.Plan
		days int
	}{
		{api.PlanFree, 1}, {api.PlanHobby, 7},
		{api.PlanPro, 30}, {api.PlanScale, 90},
	} {
		if got := tc.plan.LogArchiveRetentionDaysMax(); got != tc.days {
			t.Fatalf("%s log archive cap=%d, want %d", tc.plan, got, tc.days)
		}
		unit := "days"
		if tc.days == 1 {
			unit = "day"
		}
		if !strings.Contains(retentionLogEventsBatchSQL, fmt.Sprintf("WHEN '%s'", tc.plan)) ||
			!strings.Contains(retentionLogEventsBatchSQL, fmt.Sprintf("interval '%d %s'", tc.days, unit)) {
			t.Fatalf("retention SQL does not include %s's %d-day window", tc.plan, tc.days)
		}
	}
	if MaxLogEventRetentionDays != api.PlanScale.LogArchiveRetentionDaysMax() ||
		!strings.Contains(dropExpiredLogEventPartitionsSQL, "interval '90 days'") {
		t.Fatal("partition drop window differs from the longest plan cap")
	}
}
