// adr: 20261102183152

package meter

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type scriptedJobLogStore struct {
	results []state.JobLogRetentionResult
	err     error
	batches []state.JobLogRetentionBatch
}

func (s *scriptedJobLogStore) ClearExpiredJobLogs(_ context.Context, batch state.JobLogRetentionBatch) (state.JobLogRetentionResult, error) {
	s.batches = append(s.batches, batch)
	if s.err != nil {
		return state.JobLogRetentionResult{}, s.err
	}
	if len(s.results) == 0 {
		return state.JobLogRetentionResult{More: true, Runs: 1}, nil
	}
	got := s.results[0]
	s.results = s.results[1:]
	return got, nil
}

// adr: 20261102183152
func TestJobLogRetentionWindowsAreThePlanLogArchiveCaps(t *testing.T) {
	windows, floor := JobLogRetentionWindows()
	want := map[api.Plan]int{api.PlanFree: 1, api.PlanHobby: 7, api.PlanPro: 30, api.PlanScale: 90}
	if len(windows) != len(want) {
		t.Fatalf("windows = %+v, want one per plan", windows)
	}
	for _, w := range windows {
		if w.Days != w.Plan.LogArchiveRetentionDaysMax() || w.Days != want[w.Plan] {
			t.Fatalf("%s job logs kept %d days; its log archive cap is %d (want %d)", w.Plan, w.Days, w.Plan.LogArchiveRetentionDaysMax(), want[w.Plan])
		}
	}
	if floor != api.PlanFree.LogArchiveRetentionDaysMax() {
		t.Fatalf("floor = %d, want the shortest window (Free's %d)", floor, api.PlanFree.LogArchiveRetentionDaysMax())
	}
}

// adr: 20261102183152
func TestRetentionOnceJobLogsBatchesUntilNoRunIsDue(t *testing.T) {
	store := &scriptedJobLogStore{results: []state.JobLogRetentionResult{
		{Runs: 3, Tasks: 9000, Attempts: 9100, More: true},
		{Runs: 2, Tasks: 4, Attempts: 5},
	}}
	cleared, err := RetentionOnceJobLogs(context.Background(), store)
	if err != nil || cleared != (JobLogsCleared{Runs: 5, Tasks: 9004, Attempts: 9105}) {
		t.Fatalf("cleared = %+v, %v", cleared, err)
	}
	if len(store.batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(store.batches))
	}
	windows, floor := JobLogRetentionWindows()
	for _, b := range store.batches {
		if b.MaxRuns != JobLogRetentionRunBatch || b.MaxTasks != RetentionBatchSize || b.FloorDays != floor || len(b.Windows) != len(windows) {
			t.Fatalf("batch bounds = %+v", b)
		}
	}
}

// adr: 20261102183152
func TestRetentionOnceJobLogsStopsAtTheBatchCap(t *testing.T) {
	store := &scriptedJobLogStore{}
	cleared, err := RetentionOnceJobLogs(context.Background(), store)
	if !errors.Is(err, ErrRetentionBatchCap) || cleared.Runs != MaxRetentionBatches || len(store.batches) != MaxRetentionBatches {
		t.Fatalf("cleared = %+v after %d batches, err = %v", cleared, len(store.batches), err)
	}
}

// adr: 20261102183152
func TestRetentionOnceJobLogsReportsStoreFailures(t *testing.T) {
	boom := errors.New("database unavailable")
	cleared, err := RetentionOnceJobLogs(context.Background(), &scriptedJobLogStore{err: boom})
	if !errors.Is(err, boom) || cleared != (JobLogsCleared{}) {
		t.Fatalf("cleared = %+v, err = %v", cleared, err)
	}
}
