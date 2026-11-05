package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTenantReplayCannotOverrideDisabledBase(t *testing.T) {
	enabled, disabled := true, false
	trigger := api.WorkflowTriggerSpec{Type: "schedule", Schedule: "* * * * *", Enabled: &enabled, TenantConfigurable: true}
	definition := api.WorkflowSpec{Name: "nightly", Trigger: &trigger}
	row := WorkflowScheduleOccurrence{WorkflowName: definition.Name, DefinitionHash: workflowScheduleDefinitionHash(definition)}
	snapshot, err := encodeTenantWorkflowScheduleSnapshot(trigger, 1)
	if err != nil {
		t.Fatal(err)
	}
	definition.Trigger.Enabled = &disabled
	raw, err := json.Marshal([]api.WorkflowSpec{definition})
	if err != nil {
		t.Fatal(err)
	}
	_, outcome, err := workflowScheduleReplayDefinition(row, raw, api.PlanPro, &WorkflowScheduleCursor{TriggerSnapshot: snapshot})
	if err != nil || outcome != WorkflowScheduleReplayScheduleDisabled {
		t.Fatalf("disabled base replay: outcome=%s err=%v", outcome, err)
	}
}

func TestTenantScheduleRearmsLazilyAfterFailureResume(t *testing.T) {
	resume := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	old := resume.Add(-time.Hour)
	cursor := WorkflowScheduleCursor{DeploymentID: "deployment", LastEvaluatedAt: old, ScheduledFor: &old, Status: WorkflowScheduleStarted, TriggerSnapshot: json.RawMessage(`{"preserve":true}`)}
	rearmTenantScheduleAfterFailureResume(&cursor, resume)
	if cursor.DeploymentID != "" || cursor.Status != WorkflowScheduleArmed || cursor.ScheduledFor != nil || string(cursor.TriggerSnapshot) != `{"preserve":true}` {
		t.Fatalf("cursor was not safely rearmed: %+v", cursor)
	}
	cursor.DeploymentID = "new"
	cursor.LastEvaluatedAt = resume.Add(time.Microsecond)
	rearmTenantScheduleAfterFailureResume(&cursor, resume)
	if cursor.DeploymentID != "new" {
		t.Fatalf("already rearmed cursor changed: %+v", cursor)
	}
}

func TestTenantScheduleRearmsLazilyAfterOwnerChange(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-time.Minute)
	cursor := WorkflowScheduleCursor{DeploymentID: "deployment", UpdatedAt: old, LastEvaluatedAt: now.Add(time.Hour), ScheduledFor: &old, Status: WorkflowScheduleStarted, TriggerSnapshot: json.RawMessage(`{"preserve":true}`)}
	rearmTenantScheduleAfterOwnerChange(&cursor, now)
	if cursor.DeploymentID != "" || cursor.Status != WorkflowScheduleArmed || cursor.ScheduledFor != nil || string(cursor.TriggerSnapshot) != `{"preserve":true}` {
		t.Fatalf("owner change did not invalidate cursor: %+v", cursor)
	}
	cursor.DeploymentID, cursor.UpdatedAt = "new", now.Add(time.Second)
	rearmTenantScheduleAfterOwnerChange(&cursor, now)
	if cursor.DeploymentID != "new" {
		t.Fatalf("fresh cursor was invalidated: %+v", cursor)
	}
	cursor.DeploymentID, cursor.UpdatedAt = "equal", now
	rearmTenantScheduleAfterOwnerChange(&cursor, now)
	if cursor.DeploymentID != "" {
		t.Fatalf("equal timestamps must not hide invalidation: %+v", cursor)
	}
}
