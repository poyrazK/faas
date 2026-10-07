package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	WorkflowScheduleReplayEligible             = "eligible"
	WorkflowScheduleReplayReplayed             = "replayed"
	WorkflowScheduleReplayAlreadyReplayed      = "already_replayed"
	WorkflowScheduleReplayOccurrenceNotFound   = "occurrence_not_found"
	WorkflowScheduleReplayNotSkipped           = "not_skipped"
	WorkflowScheduleReplayHistoryNotReplayable = "history_not_replayable"
	WorkflowScheduleReplayDeploymentChanged    = "deployment_changed"
	WorkflowScheduleReplayDefinitionChanged    = "definition_changed"
	WorkflowScheduleReplayScheduleDisabled     = "schedule_disabled"
	WorkflowScheduleReplayOverlapActive        = "overlap_active"
	WorkflowScheduleReplayQuotaFull            = "quota_full"
	WorkflowScheduleReplayTenantUnavailable    = "tenant_unavailable"
	WorkflowScheduleReplayTargetUnavailable    = "target_unavailable"
	WorkflowScheduleReplayPlanUnavailable      = "plan_unavailable"
)

// WorkflowScheduleReplayResult reports the replay decision for one selected
// history row. Outcome is advisory for previews and final for replay actions.
type WorkflowScheduleReplayResult struct {
	OccurrenceID     string `json:"occurrence_id"`
	PlatformTenantID string `json:"platform_tenant_id,omitempty"`
	WorkflowName     string `json:"workflow_name,omitempty"`
	ScheduledFor     string `json:"scheduled_for,omitempty"`
	Outcome          string `json:"outcome"`
	ReplayRunID      string `json:"replay_run_id,omitempty"`
}

// WorkflowScheduleReplayStore provides a read-only eligibility preview and a
// bounded, idempotent replay action for selected schedule history rows.
type WorkflowScheduleReplayStore interface {
	PreviewWorkflowScheduleReplays(context.Context, string, []string) ([]WorkflowScheduleReplayResult, error)
	ReplayWorkflowScheduleOccurrences(context.Context, string, []string) ([]WorkflowScheduleReplayResult, error)
}

func validateWorkflowScheduleReplaySelection(appID string, ids []string) error {
	if _, err := uuid.Parse(appID); err != nil || len(ids) == 0 || len(ids) > api.WorkflowScheduleReplayBatchMax {
		return ErrInvalidArgument
	}
	seen := make(map[string]struct{}, len(ids))
	for i, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return ErrInvalidArgument
		}
		key := parsed.String()
		ids[i] = key
		if _, exists := seen[key]; exists {
			return ErrInvalidArgument
		}
		seen[key] = struct{}{}
	}
	return nil
}

func workflowScheduleReplayResult(row WorkflowScheduleOccurrence, outcome string) WorkflowScheduleReplayResult {
	result := WorkflowScheduleReplayResult{OccurrenceID: row.ID, PlatformTenantID: row.PlatformTenantID,
		WorkflowName: row.WorkflowName, Outcome: outcome, ReplayRunID: row.ReplayRunID}
	if !row.ScheduledFor.IsZero() {
		result.ScheduledFor = row.ScheduledFor.UTC().Format(time.RFC3339Nano)
	}
	return result
}

func workflowScheduleReplayOrder(rows []WorkflowScheduleOccurrence) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].ID == "" || rows[j].ID == "" {
			if rows[i].ID == "" && rows[j].ID == "" {
				return false
			}
			return rows[j].ID == ""
		}
		if !rows[i].ScheduledFor.Equal(rows[j].ScheduledFor) {
			return rows[i].ScheduledFor.Before(rows[j].ScheduledFor)
		}
		return rows[i].ID < rows[j].ID
	})
}

func workflowScheduleReplayDefinition(row WorkflowScheduleOccurrence, raw json.RawMessage, plan api.Plan, cursor *WorkflowScheduleCursor) (*api.WorkflowSpec, string, error) {
	if row.DefinitionHash == "" {
		return nil, WorkflowScheduleReplayHistoryNotReplayable, nil
	}
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(raw, &definitions); err != nil {
		return nil, "", fmt.Errorf("state: decode workflow schedule replay definition: %w", err)
	}
	var definition *api.WorkflowSpec
	for i := range definitions {
		if definitions[i].Name == row.WorkflowName {
			definition = &definitions[i]
			break
		}
	}
	if definition == nil || definition.Trigger == nil || definition.Trigger.Type != "schedule" {
		return nil, WorkflowScheduleReplayScheduleDisabled, nil
	}
	if cursor != nil {
		configured, _, customized := tenantWorkflowScheduleConfigFromSnapshot(cursor.TriggerSnapshot)
		if customized && definition.Trigger.TenantConfigurable {
			effective := applyTenantWorkflowScheduleTrigger(*definition.Trigger, configured)
			definition.Trigger = &effective
		}
	}
	if definition.Trigger.Enabled != nil && !*definition.Trigger.Enabled {
		return nil, WorkflowScheduleReplayScheduleDisabled, nil
	}
	if _, err := api.ValidateWorkflowDAG(*definition, plan); err != nil {
		return nil, WorkflowScheduleReplayDefinitionChanged, nil
	}
	if workflowScheduleDefinitionHash(*definition) != row.DefinitionHash {
		return nil, WorkflowScheduleReplayDefinitionChanged, nil
	}
	return definition, WorkflowScheduleReplayEligible, nil
}

func workflowScheduleReplayInitialOutcome(row WorkflowScheduleOccurrence) string {
	if row.ReplayRunID != "" {
		return WorkflowScheduleReplayAlreadyReplayed
	}
	if row.Status != WorkflowScheduleSkippedOverlap && row.Status != WorkflowScheduleSkippedQuota {
		return WorkflowScheduleReplayNotSkipped
	}
	if row.DefinitionHash == "" {
		return WorkflowScheduleReplayHistoryNotReplayable
	}
	return WorkflowScheduleReplayEligible
}
