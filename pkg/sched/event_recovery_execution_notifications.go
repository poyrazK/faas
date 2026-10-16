package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) runEventRecoveryExecutionNotifications(ctx context.Context, now time.Time) {
	l.pruneWorkflowRecoveryHistory(ctx, now)
	l.observeWorkflowRecoveryFailures(ctx, now)
	l.runWorkflowRecoveryPolicies(ctx, now)
	l.runWorkflowRecoveryAutomaticPolicies(ctx, now)
	l.runWorkflowInitialManualCompletions(ctx, now)
	l.runWorkflowRecoveryAutomaticBlockedNotifications(ctx, now)
	l.runWorkflowRecoveryHealthAlerts(ctx, now)
	l.runWorkflowRecoveryReviewNotifications(ctx, now)
	l.runWorkflowRecoveryReviewCompletions(ctx, now)
	if attention, ok := l.engine.store.(state.WorkflowRecoveryExecutionAttentionStore); ok {
		for i := 0; i < eventFanoutRecoveryBatch; i++ {
			worked, err := attention.ProcessNextWorkflowRecoveryExecutionAttention(ctx, now)
			if err != nil {
				if l.log != nil {
					l.log.Warn("sched: recovery execution attention failed", "err", err)
				}
				break
			}
			if !worked {
				break
			}
		}
	}
	store, ok := l.engine.store.(state.EventRecoveryExecutionNotificationStore)
	if !ok {
		return
	}
	for i := 0; i < eventFanoutRecoveryBatch; i++ {
		worked, err := store.ProcessNextEventRecoveryExecutionNotification(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: capture recovery execution notification failed", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}

func (l *Loop) runWorkflowRecoveryPolicies(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.WorkflowRecoveryPolicyStore)
	if !ok {
		return
	}
	for i := 0; i < api.WorkflowRecoveryPolicyPollBatch; i++ {
		worked, err := store.ProcessNextWorkflowRecoveryPolicy(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: recovery preview policy evaluation failed", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}

func (l *Loop) runWorkflowRecoveryReviewNotifications(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.WorkflowRecoveryReviewNotificationStore)
	if !ok {
		return
	}
	for i := 0; i < api.WorkflowRecoveryPolicyPollBatch; i++ {
		worked, err := store.ProcessNextWorkflowRecoveryReviewNotification(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: recovery review notification capture failed", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}

func (l *Loop) runWorkflowRecoveryReviewCompletions(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.WorkflowRecoveryReviewCompletionNotificationStore)
	if !ok {
		return
	}
	for i := 0; i < api.WorkflowRecoveryPolicyPollBatch; i++ {
		worked, err := store.ProcessNextWorkflowRecoveryReviewCompletion(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: review completion capture failed", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}

func (l *Loop) runWorkflowRecoveryAutomaticPolicies(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.WorkflowRecoveryAutomaticPolicyStore)
	if !ok {
		return
	}
	for i := 0; i < api.WorkflowRecoveryPolicyPollBatch; i++ {
		worked, err := store.ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: automatic recovery policy execution failed", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}

// Manual monitoring has its own allowance and cannot consume automatic work.
func (l *Loop) runWorkflowInitialManualCompletions(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.WorkflowInitialManualCompletionStore)
	if !ok {
		return
	}
	for i := 0; i < api.WorkflowInitialManualCompletionPollBatch; i++ {
		worked, err := store.ProcessNextWorkflowInitialManualCompletion(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: manual initial recovery observation failed", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}

func (l *Loop) runWorkflowRecoveryAutomaticBlockedNotifications(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.WorkflowRecoveryAutomaticBlockedStore)
	if !ok {
		return
	}
	for i := 0; i < api.WorkflowRecoveryPolicyPollBatch; i++ {
		worked, err := store.ProcessNextWorkflowRecoveryAutomaticBlockedNotification(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: blocked automatic recovery notification failed", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}
