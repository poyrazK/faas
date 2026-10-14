// adr: 1006
package sched

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"testing"
	"time"
)

type recoveryAttentionTestStore struct {
	MemStore                        *state.MemStore
	attentionCalls, completionCalls int
	fail                            bool
}

func (s *recoveryAttentionTestStore) ProcessNextWorkflowRecoveryExecutionAttention(context.Context, time.Time) (bool, error) {
	s.attentionCalls++
	if s.fail {
		return false, errors.New("attention unavailable")
	}
	return s.attentionCalls < 3, nil
}
func (s *recoveryAttentionTestStore) ProcessNextEventRecoveryExecutionNotification(context.Context, time.Time) (bool, error) {
	s.completionCalls++
	return false, nil
}
func TestWorkflowRecoveryAttentionScheduler(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := &recoveryAttentionTestStore{MemStore: state.NewMemStore(), fail: fail}
		l := &Loop{engine: &Engine{store: statetest.NewMem(s)}}
		l.runEventRecoveryExecutionNotifications(context.Background(), time.Now().UTC())
		want := 3
		if fail {
			want = 1
		}
		if s.attentionCalls != want || s.completionCalls != 1 {
			t.Fatalf("fail=%v calls=%d completion=%d", fail, s.attentionCalls, s.completionCalls)
		}
	}
}
