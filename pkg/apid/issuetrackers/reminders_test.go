package issuetrackers

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type reminderStoreFake struct {
	calls     int
	stopAfter int
	err       error
}

func (s *reminderStoreFake) EnqueueIssueTrackerReminder(context.Context) (bool, error) {
	s.calls++
	if s.err != nil {
		return false, s.err
	}
	return s.stopAfter == 0 || s.calls < s.stopAfter, nil
}
func TestReminderBatchBoundsStopsAndCancellation(t *testing.T) {
	full := &reminderStoreFake{}
	if err := EnqueueReminderBatch(t.Context(), full); err != nil || full.calls != api.IssueTrackerReminderBatch {
		t.Fatal(full, err)
	}
	empty := &reminderStoreFake{stopAfter: 2}
	if err := EnqueueReminderBatch(t.Context(), empty); err != nil || empty.calls != 2 {
		t.Fatal(empty, err)
	}
	failed := &reminderStoreFake{err: errors.New("unavailable")}
	if err := EnqueueReminderBatch(t.Context(), failed); !errors.Is(err, failed.err) || failed.calls != 1 {
		t.Fatal(failed, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	canceled := &reminderStoreFake{}
	if err := EnqueueReminderBatch(ctx, canceled); !errors.Is(err, context.Canceled) || canceled.calls != 0 {
		t.Fatal(canceled, err)
	}
}
