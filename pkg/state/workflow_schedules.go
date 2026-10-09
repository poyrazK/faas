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
	AppID            string
	PlatformTenantID string
	DeploymentID     string
	Workflows        json.RawMessage
}

type WorkflowScheduleCursor struct {
	AppID            string
	PlatformTenantID string
	WorkflowName     string
	DeploymentID     string
	TriggerSnapshot  json.RawMessage
	LastAdmittedAt   *time.Time
	LastEvaluatedAt  time.Time
	ScheduledFor     *time.Time
	Status           string
	LastRunID        string
}

type TenantWorkflowSchedule struct {
	AppID            string
	PlatformTenantID string
	WorkflowName     string
	DeploymentID     string
	Schedule         string
	Timezone         string
	Overlap          string
	CatchUp          string
	CatchUpWindow    string
	Enabled          bool
	Customized       bool
	Version          int64
	EffectiveTrigger api.WorkflowTriggerSpec `json:"-"`
	Cursor           *WorkflowScheduleCursor `json:"-"`
}

// TenantWorkflowScheduleStore admits schedule-triggered workflows once per
// active platform tenant linked to a tenant-required app and stores opt-in
// tenant cadence overrides beside each tenant's durable cursor. The app-level
// workflow concurrency quota is still shared across all of its tenants.
type TenantWorkflowScheduleStore interface {
	ListTenantWorkflowScheduleCandidates(context.Context, string, string, string, int) ([]WorkflowScheduleCandidate, error)
	AdmitTenantScheduledWorkflow(context.Context, string, string, string, string, time.Time) (WorkflowScheduleCursor, bool, error)
	ListTenantWorkflowSchedules(context.Context, string, string, string) ([]TenantWorkflowSchedule, error)
	UpdateTenantWorkflowSchedule(context.Context, string, string, string, string, int64, string, string, string, bool) (TenantWorkflowSchedule, error)
}

// FairTenantWorkflowScheduleStore orders tenant/workflow pairs by their last
// admission and excludes those already evaluated during this minute. Paging
// removes evaluated rows, so priority changes cannot move unseen rows behind
// a keyset cursor.
type FairTenantWorkflowScheduleStore interface {
	ListFairTenantWorkflowScheduleCandidates(context.Context, string, time.Time, int) ([]WorkflowScheduleCandidate, error)
}

const (
	WorkflowScheduleArmed          = "armed"
	WorkflowScheduleStarted        = "started"
	WorkflowScheduleSkippedOverlap = "skipped_overlap"
	WorkflowScheduleSkippedQuota   = "skipped_quota"
)

type tenantWorkflowScheduleConfigSnapshot struct {
	Version int64                   `json:"version"`
	Trigger api.WorkflowTriggerSpec `json:"trigger"`
}

type tenantWorkflowScheduleSnapshot struct {
	TenantConfiguration *tenantWorkflowScheduleConfigSnapshot `json:"tenant_configuration,omitempty"`
}

func tenantWorkflowScheduleConfigFromSnapshot(raw json.RawMessage) (api.WorkflowTriggerSpec, int64, bool) {
	var snapshot tenantWorkflowScheduleSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.TenantConfiguration == nil || snapshot.TenantConfiguration.Version <= 0 {
		return api.WorkflowTriggerSpec{}, 0, false
	}
	return snapshot.TenantConfiguration.Trigger, snapshot.TenantConfiguration.Version, true
}

func encodeTenantWorkflowScheduleSnapshot(trigger api.WorkflowTriggerSpec, version int64) (json.RawMessage, error) {
	return json.Marshal(tenantWorkflowScheduleSnapshot{TenantConfiguration: &tenantWorkflowScheduleConfigSnapshot{
		Version: version, Trigger: trigger,
	}})
}

func tenantConfigurableWorkflowDefinition(raw json.RawMessage, name string, plan api.Plan) (*api.WorkflowSpec, error) {
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(raw, &definitions); err != nil {
		return nil, fmt.Errorf("state: decode tenant-configurable workflow definitions: %w", err)
	}
	for _, definition := range definitions {
		if definition.Name != name {
			continue
		}
		if _, err := api.ValidateWorkflowDAG(definition, plan); err != nil {
			return nil, err
		}
		if definition.Trigger == nil || definition.Trigger.Type != "schedule" || !definition.Trigger.TenantConfigurable ||
			(definition.Trigger.Enabled != nil && !*definition.Trigger.Enabled) {
			return nil, nil
		}
		return &definition, nil
	}
	return nil, nil
}

func normalizeTenantWorkflowScheduleTrigger(base api.WorkflowTriggerSpec, schedule, timezone, overlap string, enabled bool) (api.WorkflowTriggerSpec, error) {
	if base.Type != "schedule" || !base.TenantConfigurable || (base.Enabled != nil && !*base.Enabled) {
		return api.WorkflowTriggerSpec{}, ErrInvalidArgument
	}
	trigger := base
	trigger.Schedule = schedule
	if timezone == "" {
		timezone = base.Timezone
	}
	normalizedTimezone, err := cronexpr.NormalizeTimezone(timezone)
	if err != nil {
		return api.WorkflowTriggerSpec{}, ErrInvalidArgument
	}
	trigger.Timezone = normalizedTimezone
	if overlap == "" {
		overlap = base.Overlap
	}
	if overlap == "" {
		overlap = "skip"
	}
	trigger.Overlap = overlap
	trigger.Enabled = &enabled
	if err := api.ValidateWorkflowTrigger(&trigger); err != nil {
		return api.WorkflowTriggerSpec{}, ErrInvalidArgument
	}
	return trigger, nil
}

func applyTenantWorkflowScheduleTrigger(base api.WorkflowTriggerSpec, configured api.WorkflowTriggerSpec) api.WorkflowTriggerSpec {
	base.Schedule = configured.Schedule
	base.Timezone = configured.Timezone
	base.Overlap = configured.Overlap
	if configured.Enabled == nil {
		base.Enabled = nil
	} else {
		enabled := *configured.Enabled
		base.Enabled = &enabled
	}
	return base
}

func tenantWorkflowScheduleFromDefinition(appID, tenantID, deploymentID string, definition api.WorkflowSpec, configured api.WorkflowTriggerSpec, version int64, customized bool) TenantWorkflowSchedule {
	trigger := *definition.Trigger
	if customized {
		trigger = applyTenantWorkflowScheduleTrigger(trigger, configured)
	}
	effectiveTrigger := trigger
	if trigger.Timezone == "" {
		trigger.Timezone = cronexpr.DefaultTimezone
	}
	if trigger.Overlap == "" {
		trigger.Overlap = "skip"
	}
	enabled := trigger.Enabled == nil || *trigger.Enabled
	window, _ := trigger.ScheduleCatchUpWindow() // The definition was validated before listing/updating.
	windowText := ""
	if window > 0 {
		windowText = window.String()
	}
	return TenantWorkflowSchedule{AppID: appID, PlatformTenantID: tenantID, WorkflowName: definition.Name,
		DeploymentID: deploymentID, Schedule: trigger.Schedule, Timezone: trigger.Timezone,
		Overlap: trigger.Overlap, CatchUp: trigger.ScheduleCatchUpPolicy(), CatchUpWindow: windowText,
		Enabled: enabled, Customized: customized, Version: version, EffectiveTrigger: effectiveTrigger}
}

// WorkflowScheduleCursorMatches reports whether a durable cursor belongs to
// the selected deployment and effective schedule definition, including a
// tenant override snapshot when present.
func WorkflowScheduleCursorMatches(cursor WorkflowScheduleCursor, deploymentID string, trigger api.WorkflowTriggerSpec) bool {
	if cursor.DeploymentID != deploymentID {
		return false
	}
	stored := cursor.TriggerSnapshot
	if configured, _, ok := tenantWorkflowScheduleConfigFromSnapshot(stored); ok {
		var err error
		stored, err = json.Marshal(configured)
		if err != nil {
			return false
		}
	}
	current, err := json.Marshal(trigger)
	return err == nil && equalWorkflowJSON(stored, current)
}

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

// evaluateWorkflowSchedule is shared by both stores. New/changed schedules arm
// before their first fire. Recovery selects at most one eligible occurrence.
func evaluateWorkflowSchedule(appID, tenantID, deploymentID string, spec api.WorkflowSpec, previous *WorkflowScheduleCursor, now time.Time, active, namedActive, maxActive int) (*WorkflowScheduleCursor, *WorkflowRun, error) {
	trigger, err := json.Marshal(spec.Trigger)
	if err != nil {
		return nil, nil, err
	}
	cursor := &WorkflowScheduleCursor{AppID: appID, PlatformTenantID: tenantID, WorkflowName: spec.Name,
		DeploymentID: deploymentID, TriggerSnapshot: trigger, LastEvaluatedAt: now.UTC()}
	if previous != nil {
		cursor.LastAdmittedAt = cloneTimePtr(previous.LastAdmittedAt)
	}
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
	nominal, err = workflowScheduleNominal(*spec.Trigger, previous.LastEvaluatedAt, now)
	if err != nil {
		return nil, nil, err
	}
	if nominal.IsZero() {
		value := *previous
		value.LastEvaluatedAt = now.UTC()
		return &value, nil, nil
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
	run := &WorkflowRun{AppID: appID, DeploymentID: deploymentID, PlatformTenantID: tenantID, WorkflowName: spec.Name,
		DefinitionSnapshot: definition, Input: cloneWorkflowJSON(spec.Trigger.Input), ScheduledFor: nominal}
	if err := prepareWorkflowRun(run); err != nil {
		return nil, nil, err
	}
	cursor.Status, cursor.LastRunID = WorkflowScheduleStarted, run.ID
	admitted := now.UTC().Truncate(time.Minute)
	cursor.LastAdmittedAt = &admitted
	return cursor, run, nil
}

// workflowScheduleOutcomeChanged distinguishes internal scan progress from a
// changed admission outcome. Non-due evaluations retain the public no-op result.
func workflowScheduleOutcomeChanged(next, previous *WorkflowScheduleCursor) bool {
	if previous == nil {
		return true
	}
	if next.DeploymentID != previous.DeploymentID || next.Status != previous.Status || next.LastRunID != previous.LastRunID || !equalWorkflowJSON(next.TriggerSnapshot, previous.TriggerSnapshot) {
		return true
	}
	if next.ScheduledFor == nil || previous.ScheduledFor == nil {
		return next.ScheduledFor != nil || previous.ScheduledFor != nil
	}
	return !next.ScheduledFor.Equal(*previous.ScheduledFor)
}
