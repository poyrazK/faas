package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const workflowScheduleBatch = 256

// The keyed workflow worker serializes this scan within one process. Database
// admission serializes it across scheduler replicas and manual run producers.
func (l *Loop) runWorkflowSchedulesTick(ctx context.Context) error {
	store, ok := l.engine.Store().(state.WorkflowScheduleStore)
	if !ok || !l.workflowsDispatched {
		return nil
	}
	now := l.now()
	minute := now.Unix() / 60
	if minute != l.workflowScheduleMinute {
		l.workflowScheduleMinute, l.workflowScheduleAfter, l.workflowScheduleComplete = minute, "", false
		l.workflowScheduleFailed = false
	}
	if l.workflowScheduleComplete {
		return nil
	}
	candidates, err := store.ListWorkflowScheduleCandidates(ctx, l.engine.OwnerNodeID(), l.workflowScheduleAfter, workflowScheduleBatch)
	if err != nil {
		return err
	}
	var failures []error
	for _, candidate := range candidates {
		var definitions []api.WorkflowSpec
		if err := json.Unmarshal(candidate.Workflows, &definitions); err != nil {
			failures = append(failures, fmt.Errorf("workflow schedules: decode app %s: %w", candidate.AppID, err))
			continue
		}
		for _, definition := range definitions {
			if definition.Trigger == nil || definition.Trigger.Type != "schedule" {
				continue
			}
			cursor, changed, err := store.AdmitScheduledWorkflow(ctx, candidate.AppID, candidate.DeploymentID, definition.Name, l.now())
			if err != nil {
				failures = append(failures, fmt.Errorf("workflow schedules: admit %s/%s: %w", candidate.AppID, definition.Name, err))
				continue
			}
			if changed && l.log != nil {
				l.log.Info("workflow schedule evaluated", "app_id", candidate.AppID, "workflow", definition.Name,
					"status", cursor.Status, "run_id", cursor.LastRunID, "scheduled_for", cursor.ScheduledFor)
			}
		}
	}
	l.workflowScheduleFailed = l.workflowScheduleFailed || len(failures) > 0
	if len(candidates) < workflowScheduleBatch {
		l.workflowScheduleComplete = !l.workflowScheduleFailed
		l.workflowScheduleAfter, l.workflowScheduleFailed = "", false
	} else {
		l.workflowScheduleAfter = candidates[len(candidates)-1].AppID
	}
	return errors.Join(failures...)
}
