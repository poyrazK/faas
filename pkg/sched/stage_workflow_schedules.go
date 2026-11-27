package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// stageWorkflowSchedulePass is the keyset position of the stage schedule
// pass (ADR-20261104120000): a stage's enabled schedule workflows run on the
// stage's own live deployment. A pass that spans minutes finishes before it
// restarts, as production's does.
type stageWorkflowSchedulePass struct {
	minute             int64
	afterApp, afterEnv string
	complete, failed   bool
}

func (l *Loop) runStageWorkflowSchedulesTick(ctx context.Context) error {
	store, ok := l.engine.Store().(state.StageWorkflowScheduleStore)
	if !ok {
		return nil
	}
	stored, ok := l.engine.Store().(state.StoredWorkflowStore)
	if !ok {
		return nil
	}
	now := l.now()
	pass := &l.stageWorkflowSchedules
	if minute := now.Unix() / 60; minute != pass.minute {
		pass.minute = minute
		if pass.complete {
			pass.afterApp, pass.afterEnv, pass.failed = "", "", false
		}
		pass.complete = false
	}
	if pass.complete {
		return nil
	}
	candidates, err := store.ListStageWorkflowScheduleCandidates(ctx, l.engine.OwnerNodeID(), pass.afterApp, pass.afterEnv, workflowScheduleBatch)
	if err != nil {
		return err
	}
	var failures []error
	for _, candidate := range candidates {
		if l.engine.capabilities != nil && !l.engine.capabilities.EveryAccount("workflows") && !l.workflowCapabilityAllowed(ctx, candidate.AppID) {
			continue
		}
		records, err := stored.ListStageStoredWorkflows(ctx, candidate.AppID, candidate.EnvironmentID)
		if err != nil {
			failures = append(failures, fmt.Errorf("stage workflow schedules: list %s/%s: %w", candidate.AppID, candidate.EnvironmentID, err))
			continue
		}
		for _, record := range records {
			if !record.Enabled || len(record.Published) == 0 {
				continue
			}
			var definition api.WorkflowSpec
			if json.Unmarshal(record.Published, &definition) != nil || definition.Trigger == nil || definition.Trigger.Type != "schedule" {
				continue
			}
			cursor, changed, err := store.AdmitStageScheduledWorkflow(ctx, candidate.AppID, candidate.EnvironmentID, candidate.DeploymentID, record.Name, now)
			if err != nil {
				failures = append(failures, fmt.Errorf("stage workflow schedules: admit %s/%s/%s: %w", candidate.AppID, candidate.EnvironmentID, record.Name, err))
				continue
			}
			if changed && l.log != nil {
				l.log.Info("stage workflow schedule evaluated", "app_id", candidate.AppID, "environment_id", candidate.EnvironmentID,
					"workflow", record.Name, "status", cursor.Status, "run_id", cursor.LastRunID, "scheduled_for", cursor.ScheduledFor)
			}
		}
	}
	pass.failed = pass.failed || len(failures) > 0
	if len(candidates) < workflowScheduleBatch {
		pass.complete = !pass.failed
		pass.afterApp, pass.afterEnv, pass.failed = "", "", false
	} else {
		last := candidates[len(candidates)-1]
		pass.afterApp, pass.afterEnv = last.AppID, last.EnvironmentID
	}
	return errors.Join(failures...)
}
