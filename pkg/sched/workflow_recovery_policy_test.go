// adr: 1009
package sched

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

type recoveryPolicySchedulerStore struct {
	*state.MemStore
	calls, completion int
	fail              bool
}

func (s *recoveryPolicySchedulerStore) ProcessNextWorkflowRecoveryPolicy(context.Context, time.Time) (bool, error) {
	s.calls++
	if s.fail {
		return false, errors.New("policy unavailable")
	}
	return true, nil
}
func (s *recoveryPolicySchedulerStore) ProcessNextEventRecoveryExecutionNotification(context.Context, time.Time) (bool, error) {
	s.completion++
	return false, nil
}
func TestWorkflowRecoveryPolicyScheduler(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := &recoveryPolicySchedulerStore{MemStore: state.NewMemStore(), fail: fail}
		l := &Loop{engine: &Engine{store: s}}
		l.runEventRecoveryExecutionNotifications(context.Background(), time.Now().UTC())
		want := api.WorkflowRecoveryPolicyPollBatch
		if fail {
			want = 1
		}
		if s.calls != want || s.completion != 1 {
			t.Fatalf("calls=%d completion=%d", s.calls, s.completion)
		}
	}
}

type recoveryReviewNotificationSchedulerStore struct {
	*recoveryPolicySchedulerStore
	notices     int
	failNotices bool
}

func (s *recoveryReviewNotificationSchedulerStore) ProcessNextWorkflowRecoveryReviewNotification(context.Context, time.Time) (bool, error) {
	s.notices++
	if s.failNotices {
		return false, errors.New("notification capture unavailable")
	}
	return true, nil
}
func TestWorkflowRecoveryReviewNotificationScheduler(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := &recoveryReviewNotificationSchedulerStore{recoveryPolicySchedulerStore: &recoveryPolicySchedulerStore{MemStore: state.NewMemStore()}, failNotices: fail}
		l := &Loop{engine: &Engine{store: s}}
		l.runEventRecoveryExecutionNotifications(context.Background(), time.Now().UTC())
		want := api.WorkflowRecoveryPolicyPollBatch
		if fail {
			want = 1
		}
		if s.notices != want || s.completion != 1 {
			t.Fatalf("notices=%d completion=%d", s.notices, s.completion)
		}
	}
}

type recoveryReviewCompletionSchedulerStore struct {
	*recoveryReviewNotificationSchedulerStore
	finished     int
	failFinished bool
}

func (s *recoveryReviewCompletionSchedulerStore) ProcessNextWorkflowRecoveryReviewCompletion(context.Context, time.Time) (bool, error) {
	s.finished++
	if s.failFinished {
		return false, errors.New("completion capture unavailable")
	}
	return true, nil
}
func TestWorkflowRecoveryReviewCompletionScheduler(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := &recoveryReviewCompletionSchedulerStore{recoveryReviewNotificationSchedulerStore: &recoveryReviewNotificationSchedulerStore{recoveryPolicySchedulerStore: &recoveryPolicySchedulerStore{MemStore: state.NewMemStore()}, failNotices: true}, failFinished: fail}
		l := &Loop{engine: &Engine{store: s}}
		l.runEventRecoveryExecutionNotifications(context.Background(), time.Now().UTC())
		want := api.WorkflowRecoveryPolicyPollBatch
		if fail {
			want = 1
		}
		if s.finished != want || s.notices != 1 || s.completion != 1 {
			t.Fatalf("finished=%d notices=%d execution=%d", s.finished, s.notices, s.completion)
		}
	}
}

type automaticRecoverySchedulerStore struct {
	*recoveryPolicySchedulerStore
	automatic     int
	failAutomatic bool
}

func (s *automaticRecoverySchedulerStore) ProcessNextWorkflowRecoveryAutomaticPolicy(context.Context, time.Time) (bool, error) {
	s.automatic++
	if s.failAutomatic {
		return false, errors.New("automatic execution unavailable")
	}
	return true, nil
}
func TestWorkflowRecoveryAutomaticPolicyScheduler(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := &automaticRecoverySchedulerStore{recoveryPolicySchedulerStore: &recoveryPolicySchedulerStore{MemStore: state.NewMemStore()}, failAutomatic: fail}
		l := &Loop{engine: &Engine{store: s}}
		l.runEventRecoveryExecutionNotifications(context.Background(), time.Now().UTC())
		want := api.WorkflowRecoveryPolicyPollBatch
		if fail {
			want = 1
		}
		if s.automatic != want || s.completion != 1 {
			t.Fatalf("automatic=%d completion=%d", s.automatic, s.completion)
		}
	}
}

type blockedRecoverySchedulerStore struct {
	*recoveryPolicySchedulerStore
	blocked     int
	failBlocked bool
}

func (s *blockedRecoverySchedulerStore) ProcessNextWorkflowRecoveryAutomaticBlockedNotification(context.Context, time.Time) (bool, error) {
	s.blocked++
	if s.failBlocked {
		return false, errors.New("blocked capture unavailable")
	}
	return true, nil
}
func TestWorkflowRecoveryAutomaticBlockedScheduler(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := &blockedRecoverySchedulerStore{recoveryPolicySchedulerStore: &recoveryPolicySchedulerStore{MemStore: state.NewMemStore()}, failBlocked: fail}
		l := &Loop{engine: &Engine{store: s}}
		l.runEventRecoveryExecutionNotifications(context.Background(), time.Now().UTC())
		want := api.WorkflowRecoveryPolicyPollBatch
		if fail {
			want = 1
		}
		if s.blocked != want || s.completion != 1 {
			t.Fatalf("blocked %d completion %d", s.blocked, s.completion)
		}
	}
}
