package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cronexpr"
)

// WorkflowScheduleStore atomically consumes a nominal schedule minute and
// admits a run using the same per-app quota lock as manual workflow starts.
type WorkflowScheduleStore interface {
	ListWorkflowScheduleCandidates(context.Context, string, string, int) ([]WorkflowScheduleCandidate, error)
	AdmitScheduledWorkflow(context.Context, string, string, string, time.Time) (WorkflowScheduleCursor, bool, error)
	ListWorkflowScheduleCursors(context.Context, string) ([]WorkflowScheduleCursor, error)
}

type WorkflowScheduleCandidate struct {
	AppID        string
	DeploymentID string
	Workflows    json.RawMessage
}

type WorkflowScheduleCursor struct {
	AppID           string
	WorkflowName    string
	DeploymentID    string
	TriggerSnapshot json.RawMessage
	LastEvaluatedAt time.Time
	ScheduledFor    *time.Time
	Status          string
	LastRunID       string
}

const (
	WorkflowScheduleArmed          = "armed"
	WorkflowScheduleStarted        = "started"
	WorkflowScheduleSkippedOverlap = "skipped_overlap"
	WorkflowScheduleSkippedQuota   = "skipped_quota"
)

func scheduledWorkflowDefinition(raw json.RawMessage, name string, plan api.Plan) (*api.WorkflowSpec, error) {
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(raw, &definitions); err != nil {
		return nil, fmt.Errorf("state: decode scheduled workflow definitions: %w", err)
	}
	for _, definition := range definitions {
		if definition.Name != name {
			continue
		}
		if _, err := api.ValidateWorkflowDAG(definition, plan); err != nil {
			return nil, err
		}
		if definition.Trigger == nil || definition.Trigger.Type != "schedule" ||
			(definition.Trigger.Enabled != nil && !*definition.Trigger.Enabled) {
			return nil, nil
		}
		return &definition, nil
	}
	return nil, nil
}

// evaluateWorkflowSchedule is shared by both stores. New/redeployed schedules
// arm before their first fire. Only the current nominal minute is eligible:
// downtime never becomes an unbounded catch-up queue.
func evaluateWorkflowSchedule(appID, deploymentID string, spec api.WorkflowSpec, previous *WorkflowScheduleCursor, now time.Time, active, namedActive, maxActive int) (*WorkflowScheduleCursor, *WorkflowRun, error) {
	trigger, err := json.Marshal(spec.Trigger)
	if err != nil {
		return nil, nil, err
	}
	cursor := &WorkflowScheduleCursor{AppID: appID, WorkflowName: spec.Name,
		DeploymentID: deploymentID, TriggerSnapshot: trigger, LastEvaluatedAt: now.UTC()}
	if previous == nil || previous.DeploymentID != deploymentID || !equalWorkflowJSON(previous.TriggerSnapshot, trigger) {
		cursor.Status = WorkflowScheduleArmed
		if previous != nil && previous.LastEvaluatedAt.After(cursor.LastEvaluatedAt) {
			cursor.LastEvaluatedAt = previous.LastEvaluatedAt
		}
		return cursor, nil, nil
	}
	nominal := now.UTC().Truncate(time.Minute)
	if !nominal.After(previous.LastEvaluatedAt) {
		return nil, nil, nil
	}
	schedule, err := cronexpr.Parse(spec.Trigger.Schedule, spec.Trigger.Timezone)
	if err != nil {
		return nil, nil, err
	}
	if !schedule.Next(nominal.Add(-time.Minute)).Equal(nominal) {
		return nil, nil, nil
	}
	cursor.ScheduledFor = &nominal
	if spec.Trigger.Overlap != "allow" && namedActive > 0 {
		cursor.Status = WorkflowScheduleSkippedOverlap
		return cursor, nil, nil
	}
	if maxActive <= 0 || active >= maxActive {
		cursor.Status = WorkflowScheduleSkippedQuota
		return cursor, nil, nil
	}
	definition, err := json.Marshal(spec)
	if err != nil {
		return nil, nil, err
	}
	run := &WorkflowRun{AppID: appID, WorkflowName: spec.Name,
		DefinitionSnapshot: definition, Input: cloneWorkflowJSON(spec.Trigger.Input), ScheduledFor: nominal}
	if err := prepareWorkflowRun(run); err != nil {
		return nil, nil, err
	}
	cursor.Status, cursor.LastRunID = WorkflowScheduleStarted, run.ID
	return cursor, run, nil
}
