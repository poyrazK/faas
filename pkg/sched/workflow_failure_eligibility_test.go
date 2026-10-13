// adr: 1026
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type failureEligibilitySchedulerStore struct {
	MemStore  *state.MemStore
	calls     int
	fail      bool
	available int
}

func (s *failureEligibilitySchedulerStore) ObserveNextWorkflowFailureEligibility(context.Context, time.Time) (bool, error) {
	s.calls++
	if s.fail {
		return false, errors.New("unavailable")
	}
	return s.calls <= s.available, nil
}
func TestWorkflowFailureEligibilityScheduler(t *testing.T) {
	for _, tt := range []struct {
		available, want int
		fail            bool
	}{{0, 1, false}, {2, 3, false}, {api.WorkflowRecoveryFailureObserverBatch + 1, api.WorkflowRecoveryFailureObserverBatch, false}, {2, 1, true}} {
		s := &failureEligibilitySchedulerStore{MemStore: state.NewMemStore(), available: tt.available, fail: tt.fail}
		l := &Loop{engine: &Engine{store: statetest.NewMem(s)}}
		l.runEventRecoveryExecutionNotifications(context.Background(), time.Now().UTC())
		if s.calls != tt.want {
			t.Fatalf("calls %d want %d", s.calls, tt.want)
		}
	}
}
