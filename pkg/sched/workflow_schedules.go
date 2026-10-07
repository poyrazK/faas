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
	if !l.workflowsDispatched {
		return nil
	}
	return errors.Join(l.runApplicationWorkflowSchedulesTick(ctx), l.runTenantWorkflowSchedulesTick(ctx))
}

func (l *Loop) runApplicationWorkflowSchedulesTick(ctx context.Context) error {
	store, ok := l.engine.Store().(state.WorkflowScheduleStore)
	if !ok {
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

func (l *Loop) runTenantWorkflowSchedulesTick(ctx context.Context) error {
	store, ok := l.engine.Store().(state.TenantWorkflowScheduleStore)
	if !ok {
		return nil
	}
	now := l.now()
	minute := now.Unix() / 60
	if minute != l.tenantWorkflowScheduleMinute {
		l.tenantWorkflowScheduleMinute = minute
		l.tenantWorkflowScheduleAfterApp, l.tenantWorkflowScheduleAfterTenant = "", ""
		l.tenantWorkflowScheduleComplete, l.tenantWorkflowScheduleFailed = false, false
	}
	if l.tenantWorkflowScheduleComplete {
		return nil
	}
	candidates, err := store.ListTenantWorkflowScheduleCandidates(ctx, l.engine.OwnerNodeID(),
		l.tenantWorkflowScheduleAfterApp, l.tenantWorkflowScheduleAfterTenant, workflowScheduleBatch)
	if err != nil {
		return err
	}
	var failures []error
	for _, candidate := range candidates {
		var definitions []api.WorkflowSpec
		if err := json.Unmarshal(candidate.Workflows, &definitions); err != nil {
			failures = append(failures, fmt.Errorf("tenant workflow schedules: decode app %s: %w", candidate.AppID, err))
			continue
		}
		for _, definition := range definitions {
			if definition.Trigger == nil || definition.Trigger.Type != "schedule" {
				continue
			}
			cursor, changed, err := store.AdmitTenantScheduledWorkflow(ctx, candidate.AppID, candidate.PlatformTenantID,
				candidate.DeploymentID, definition.Name, l.now())
			if err != nil {
				failures = append(failures, fmt.Errorf("tenant workflow schedules: admit %s/%s/%s: %w",
					candidate.AppID, candidate.PlatformTenantID, definition.Name, err))
				continue
			}
			if changed && l.log != nil {
				l.log.Info("tenant workflow schedule evaluated", "app_id", candidate.AppID,
					"platform_tenant_id", candidate.PlatformTenantID, "workflow", definition.Name,
					"status", cursor.Status, "run_id", cursor.LastRunID, "scheduled_for", cursor.ScheduledFor)
			}
		}
	}
	l.tenantWorkflowScheduleFailed = l.tenantWorkflowScheduleFailed || len(failures) > 0
	if len(candidates) < workflowScheduleBatch {
		l.tenantWorkflowScheduleComplete = !l.tenantWorkflowScheduleFailed
		if l.tenantWorkflowScheduleComplete {
			l.tenantWorkflowScheduleAfterApp, l.tenantWorkflowScheduleAfterTenant = "", ""
		} else {
			l.tenantWorkflowScheduleAfterApp, l.tenantWorkflowScheduleAfterTenant = "", ""
			l.tenantWorkflowScheduleFailed = false
		}
	} else {
		last := candidates[len(candidates)-1]
		l.tenantWorkflowScheduleAfterApp, l.tenantWorkflowScheduleAfterTenant = last.AppID, last.PlatformTenantID
	}
	return errors.Join(failures...)
}
