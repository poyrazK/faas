package issuetrackers

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type escalationStoreFake struct {
	calls     int
	stopAfter int
	err       error
}

func (s *escalationStoreFake) EnqueueIssueTrackerEscalation(context.Context) (bool, error) {
	s.calls++
	if s.err != nil {
		return false, s.err
	}
	return s.stopAfter == 0 || s.calls < s.stopAfter, nil
}
func TestEscalationBatchBoundsStopsAndCancellation(t *testing.T) {
	full := &escalationStoreFake{}
	if err := EnqueueEscalationBatch(t.Context(), full); err != nil || full.calls != api.IssueTrackerReminderBatch {
		t.Fatal(full, err)
	}
	empty := &escalationStoreFake{stopAfter: 2}
	if err := EnqueueEscalationBatch(t.Context(), empty); err != nil || empty.calls != 2 {
		t.Fatal(empty, err)
	}
	failed := &escalationStoreFake{err: errors.New("unavailable")}
	if err := EnqueueEscalationBatch(t.Context(), failed); !errors.Is(err, failed.err) || failed.calls != 1 {
		t.Fatal(failed, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	canceled := &escalationStoreFake{}
	if err := EnqueueEscalationBatch(ctx, canceled); !errors.Is(err, context.Canceled) || canceled.calls != 0 {
		t.Fatal(canceled, err)
	}
}
