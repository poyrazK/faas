// adr: 1009
package sched

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"testing"
	"time"
)

type recoveryPolicySchedulerStore struct {
	MemStore          *state.MemStore
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
		l := &Loop{engine: &Engine{store: statetest.NewMem(s)}}
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
		l := &Loop{engine: &Engine{store: statetest.NewMem(s)}}
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
		l := &Loop{engine: &Engine{store: statetest.NewMem(s)}}
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
		l := &Loop{engine: &Engine{store: statetest.NewMem(s)}}
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
		l := &Loop{engine: &Engine{store: statetest.NewMem(s)}}
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

type isolatedRecoverySchedulerStore struct {
	*recoveryPolicySchedulerStore
	manual, automatic           int
	manualError, automaticError bool
	manualWork, automaticWork   bool
}

func (s *isolatedRecoverySchedulerStore) ProcessNextWorkflowInitialManualCompletion(context.Context, time.Time) (bool, error) {
	s.manual++
	if s.manualError {
		return false, errors.New("manual observation unavailable")
	}
	return s.manualWork, nil
}
func (s *isolatedRecoverySchedulerStore) ProcessNextWorkflowRecoveryAutomaticPolicy(context.Context, time.Time) (bool, error) {
	s.automatic++
	if s.automaticError {
		return false, errors.New("automatic execution unavailable")
	}
	return s.automaticWork, nil
}

// adr: 1278
func TestWorkflowRecoverySchedulerIndependentBatches(t *testing.T) {
	cases := []struct {
		name                                                   string
		manualError, automaticError, manualWork, automaticWork bool
	}{
		{name: "sustained_backlogs", manualWork: true, automaticWork: true},
		{name: "manual_failure", manualError: true, automaticWork: true},
		{name: "automatic_failure", automaticError: true, manualWork: true},
		{name: "manual_idle", automaticWork: true},
		{name: "automatic_idle", manualWork: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &isolatedRecoverySchedulerStore{recoveryPolicySchedulerStore: &recoveryPolicySchedulerStore{MemStore: state.NewMemStore()}, manualError: tc.manualError, automaticError: tc.automaticError, manualWork: tc.manualWork, automaticWork: tc.automaticWork}
			l := &Loop{engine: &Engine{store: statetest.NewMem(s)}}
			manualPerPass, automaticPerPass := 1, 1
			if tc.manualWork {
				manualPerPass = api.WorkflowInitialManualCompletionPollBatch
			}
			if tc.automaticWork {
				automaticPerPass = api.WorkflowRecoveryPolicyPollBatch
			}
			for pass := 1; pass <= 3; pass++ {
				l.runEventRecoveryExecutionNotifications(context.Background(), time.Now().UTC())
				if s.manual != pass*manualPerPass || s.automatic != pass*automaticPerPass || s.completion != pass {
					t.Fatalf("pass %d manual=%d automatic=%d downstream=%d", pass, s.manual, s.automatic, s.completion)
				}
			}
		})
	}
}
