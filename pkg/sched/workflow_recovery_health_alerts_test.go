// adr: 1019
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

type healthAlertSchedulerStore struct {
	MemStore *state.MemStore
	calls    int
	fail     bool
}

func (s *healthAlertSchedulerStore) ProcessNextWorkflowRecoveryHealthAlert(context.Context, time.Time) (bool, error) {
	s.calls++
	if s.fail {
		return false, errors.New("unavailable")
	}
	return true, nil
}
func TestWorkflowRecoveryHealthAlertScheduler(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := &healthAlertSchedulerStore{MemStore: state.NewMemStore(), fail: fail}
		l := &Loop{engine: &Engine{store: statetest.NewMem(s)}}
		l.runWorkflowRecoveryHealthAlerts(context.Background(), time.Now().UTC())
		want := api.WorkflowRecoveryPolicyPollBatch
		if fail {
			want = 1
		}
		if s.calls != want {
			t.Fatalf("calls %d", s.calls)
		}
	}
}
