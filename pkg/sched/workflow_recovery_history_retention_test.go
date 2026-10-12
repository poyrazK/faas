// adr: 1021
package sched

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

type recoveryHistoryRetentionSchedulerStore struct {
	*state.MemStore
	calls int
	fail  bool
}

func (s *recoveryHistoryRetentionSchedulerStore) PruneWorkflowRecoveryHistory(context.Context, time.Time) (int64, error) {
	s.calls++
	if s.fail {
		return 0, errors.New("unavailable")
	}
	return 1, nil
}
func TestWorkflowRecoveryHistoryRetentionScheduler(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := &recoveryHistoryRetentionSchedulerStore{MemStore: state.NewMemStore(), fail: fail}
		l := &Loop{engine: &Engine{store: s}}
		l.runEventRecoveryExecutionNotifications(context.Background(), time.Now().UTC())
		if s.calls != 1 {
			t.Fatalf("retention calls %d", s.calls)
		}
	}
}
