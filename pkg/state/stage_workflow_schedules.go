package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Stage schedules (ADR-20261104120000): a stage's enabled schedule workflows
// run on the stage's live deployment, admitted under the app's run quota like
// production's. Their cursors live in stage_workflow_schedule_cursors, out of
// reach of production's cursor pruning (which N-1 schedd also runs).

// StageWorkflowScheduleCandidate is one app's stage holding enabled schedule
// workflows, with the stage's live deployment.
type StageWorkflowScheduleCandidate struct {
	AppID, EnvironmentID, DeploymentID string
}

// StageWorkflowScheduleStore lists and admits a stage's schedules.
type StageWorkflowScheduleStore interface {
	// ListStageWorkflowScheduleCandidates pages by (app, environment) after
	// afterApp/afterEnvironment.
	ListStageWorkflowScheduleCandidates(ctx context.Context, owner, afterApp, afterEnvironment string, limit int) ([]StageWorkflowScheduleCandidate, error)
	AdmitStageScheduledWorkflow(ctx context.Context, appID, environmentID, deploymentID, name string, now time.Time) (WorkflowScheduleCursor, bool, error)
}

// stageScheduleDefinitions is the stage's published workflows with the
// stored enabled state applied to non-manual triggers.
func stageScheduleDefinitions(records []StoredWorkflow) (json.RawMessage, error) {
	return mergeStoredWorkflowDefinitions(nil, records)
}

func (s *PgStore) ListStageWorkflowScheduleCandidates(ctx context.Context, owner, afterApp, afterEnvironment string, limit int) ([]StageWorkflowScheduleCandidate, error) {
	rows, err := sqlc.New().ListStageWorkflowScheduleCandidates(ctx, s.pool, sqlc.ListStageWorkflowScheduleCandidatesParams{
		OwnerNodeID: nullablePgUUID(owner), AfterAppID: nullablePgUUID(afterApp), AfterEnvironmentID: nullablePgUUID(afterEnvironment), BatchLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list stage workflow schedules: %w", err)
	}
	result := make([]StageWorkflowScheduleCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, StageWorkflowScheduleCandidate{AppID: pgUUIDStringOrEmpty(row.AppID),
			EnvironmentID: pgUUIDStringOrEmpty(row.EnvironmentID), DeploymentID: pgUUIDStringOrEmpty(row.DeploymentID)})
	}
	return result, nil
}

func stageWorkflowScheduleCursorFromSQL(row sqlc.StageWorkflowScheduleCursor) WorkflowScheduleCursor {
	return WorkflowScheduleCursor{AppID: pgUUIDStringOrEmpty(row.AppID), WorkflowName: row.WorkflowName,
		DeploymentID: pgUUIDStringOrEmpty(row.DeploymentID), TriggerSnapshot: cloneWorkflowJSON(row.TriggerSnapshot),
		LastAdmittedAt: timestamptzToTimePtr(row.LastAdmittedAt), LastEvaluatedAt: timeFromPgtype(row.LastEvaluatedAt),
		ScheduledFor: timestamptzToTimePtr(row.ScheduledFor), Status: row.Status, LastRunID: pgUUIDStringOrEmpty(row.LastRunID)}
}

func (s *PgStore) AdmitStageScheduledWorkflow(ctx context.Context, appID, environmentID, deploymentID, name string, now time.Time) (WorkflowScheduleCursor, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: begin stage scheduled workflow: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: lock stage scheduled workflow: %w", err)
	}
	target, err := q.LockStageWorkflowScheduleTarget(ctx, tx, sqlc.LockStageWorkflowScheduleTargetParams{AppID: mustPgUUID(appID), EnvironmentID: mustPgUUID(environmentID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowScheduleCursor{}, false, nil
	}
	if err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: resolve stage scheduled workflow target: %w", err)
	}
	plan := api.Plan(target.Plan)
	if pgUUIDStringOrEmpty(target.DeploymentID) != deploymentID || !plan.WorkflowsAllowed() {
		return WorkflowScheduleCursor{}, false, nil
	}
	definition, err := scheduledWorkflowDefinition(target.Workflows, name, plan)
	if err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	if err := q.PruneStageWorkflowScheduleCursors(ctx, tx, sqlc.PruneStageWorkflowScheduleCursorsParams{EnvironmentID: mustPgUUID(environmentID), Workflows: target.Workflows}); err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: prune retired stage workflow schedules: %w", err)
	}
	if definition == nil {
		return WorkflowScheduleCursor{}, false, tx.Commit(ctx)
	}
	var previous *WorkflowScheduleCursor
	row, err := q.GetStageWorkflowScheduleCursor(ctx, tx, sqlc.GetStageWorkflowScheduleCursorParams{EnvironmentID: mustPgUUID(environmentID), WorkflowName: name})
	if err == nil {
		stored := stageWorkflowScheduleCursorFromSQL(row)
		previous = &stored
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: get stage workflow schedule cursor: %w", err)
	}
	active, err := q.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(appID), ParkedMax: int64(plan.WorkflowMaxParkedRuns())})
	if err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	named, err := q.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(appID), WorkflowName: name})
	if err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	next, run, err := evaluateWorkflowSchedule(appID, "", deploymentID, *definition, previous, now, int(active), int(named), plan.WorkflowMaxConcurrentRuns())
	if err != nil || next == nil {
		return WorkflowScheduleCursor{}, false, err
	}
	if run != nil {
		if _, err := q.InsertScheduledWorkflowRun(ctx, tx, sqlc.InsertScheduledWorkflowRunParams{
			DeploymentID: mustPgUUID(run.DeploymentID), ID: mustPgUUID(run.ID), AppID: mustPgUUID(appID), WorkflowName: name, Input: run.Input,
			DefinitionSnapshot: run.DefinitionSnapshot, ScheduledFor: pgtype.Timestamptz{Time: run.ScheduledFor, Valid: true},
		}); err != nil {
			return WorkflowScheduleCursor{}, false, fmt.Errorf("state: insert stage scheduled workflow run: %w", err)
		}
	}
	if err := q.UpsertStageWorkflowScheduleCursor(ctx, tx, sqlc.UpsertStageWorkflowScheduleCursorParams{
		EnvironmentID: mustPgUUID(environmentID), WorkflowName: name, AppID: mustPgUUID(appID), DeploymentID: nullablePgUUID(deploymentID),
		TriggerSnapshot: next.TriggerSnapshot, LastEvaluatedAt: pgtype.Timestamptz{Time: next.LastEvaluatedAt, Valid: true},
		ScheduledFor: nullableTimestamptzPtr(next.ScheduledFor), Status: next.Status, LastRunID: nullablePgUUID(next.LastRunID),
		LastAdmittedAt: nullableTimestamptzPtr(next.LastAdmittedAt),
	}); err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: record stage scheduled workflow outcome: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: commit stage scheduled workflow: %w", err)
	}
	return *next, workflowScheduleOutcomeChanged(next, previous), nil
}

// stageScheduleTargetLocked is the stage's live deployment, the app's account
// and the stage's runnable definitions, or false.
func (m *MemStore) stageScheduleTargetLocked(appID, environmentID string) (Deployment, Account, json.RawMessage, bool) {
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted || app.MaintenanceMode || app.PlatformTenantRequired {
		return Deployment{}, Account{}, nil, false
	}
	env, ok := m.projectEnvironments[environmentID]
	if !ok || !m.storedWorkflowEnvironmentValidLocked(app, environmentID) {
		return Deployment{}, Account{}, nil, false
	}
	account, ok := m.accounts[app.AccountID]
	if !ok || !account.Active() || !account.Plan.WorkflowsAllowed() {
		return Deployment{}, Account{}, nil, false
	}
	var deployment Deployment
	for _, candidate := range m.deployments {
		if candidate.AppID == appID && candidate.Status == DeployLive && candidate.Scope == env.Slug && (deployment.ID == "" || deploymentPreferredForWake(candidate, deployment)) {
			deployment = candidate
		}
	}
	definitions, err := stageScheduleDefinitions(m.storedWorkflowStageRecordsLocked(appID, environmentID))
	if err != nil || deployment.ID == "" {
		return Deployment{}, Account{}, nil, false
	}
	return deployment, account, definitions, true
}

func (m *MemStore) ListStageWorkflowScheduleCandidates(_ context.Context, owner, afterApp, afterEnvironment string, limit int) ([]StageWorkflowScheduleCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[[2]string]bool{}
	result := make([]StageWorkflowScheduleCandidate, 0)
	for _, record := range m.storedWorkflows {
		key := [2]string{record.AppID, record.EnvironmentID}
		if record.EnvironmentID == "" || !record.Enabled || seen[key] || !storedWorkflowIsSchedule(record.Published) {
			continue
		}
		if record.AppID < afterApp || record.AppID == afterApp && record.EnvironmentID <= afterEnvironment {
			continue
		}
		if app := m.apps[record.AppID]; owner != "" && app.NodeID != owner {
			continue
		}
		deployment, _, _, ok := m.stageScheduleTargetLocked(record.AppID, record.EnvironmentID)
		if !ok {
			continue
		}
		seen[key] = true
		result = append(result, StageWorkflowScheduleCandidate{AppID: record.AppID, EnvironmentID: record.EnvironmentID, DeploymentID: deployment.ID})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AppID != result[j].AppID {
			return result[i].AppID < result[j].AppID
		}
		return result[i].EnvironmentID < result[j].EnvironmentID
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func storedWorkflowIsSchedule(published json.RawMessage) bool {
	var definition struct{ Trigger *struct{ Type string } }
	return len(published) > 0 && json.Unmarshal(published, &definition) == nil && definition.Trigger != nil && definition.Trigger.Type == "schedule"
}

func (m *MemStore) AdmitStageScheduledWorkflow(_ context.Context, appID, environmentID, deploymentID, name string, now time.Time) (WorkflowScheduleCursor, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deployment, account, definitions, ok := m.stageScheduleTargetLocked(appID, environmentID)
	if !ok || deployment.ID != deploymentID {
		return WorkflowScheduleCursor{}, false, nil
	}
	definition, err := scheduledWorkflowDefinition(definitions, name, account.Plan)
	if err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	key := environmentID + "/" + name
	if definition == nil {
		delete(m.stageWorkflowSchedules, key)
		return WorkflowScheduleCursor{}, false, nil
	}
	var admission workflowAdmission
	named := 0
	for _, run := range m.workflowRuns {
		if run.AppID == appID && (run.Status == WorkflowRunStatusPending || run.Status == WorkflowRunStatusRunning || run.Status == WorkflowRunStatusAwaitingEvent) {
			admission.add(run.Status)
			if run.WorkflowName == name {
				named++
			}
		}
	}
	var previous *WorkflowScheduleCursor
	if stored, exists := m.stageWorkflowSchedules[key]; exists {
		previous = &stored
	}
	cursor, run, err := evaluateWorkflowSchedule(appID, "", deploymentID, *definition, previous, now, admission.admitted(account.Plan.WorkflowMaxParkedRuns()), named, account.Plan.WorkflowMaxConcurrentRuns())
	if err != nil || cursor == nil {
		return WorkflowScheduleCursor{}, false, err
	}
	if run != nil {
		if err := m.insertWorkflowRunLocked(run); err != nil {
			return WorkflowScheduleCursor{}, false, err
		}
	}
	if m.stageWorkflowSchedules == nil {
		m.stageWorkflowSchedules = make(map[string]WorkflowScheduleCursor)
	}
	m.stageWorkflowSchedules[key] = *cursor
	return *cursor, workflowScheduleOutcomeChanged(cursor, previous), nil
}

// deleteStageWorkflowScheduleLocked drops a stage workflow's cursor when it is
// published, enabled, disabled or deleted, as production's are.
func (m *MemStore) deleteStageWorkflowScheduleLocked(environmentID, name string) {
	delete(m.stageWorkflowSchedules, environmentID+"/"+name)
}

// OverlayStageStoredWorkflows puts a stage's published workflows that the
// stage runs (manual and schedule) over the definitions a run in that stage
// snapshots; production's stay visible, as before stages had their own.
func OverlayStageStoredWorkflows(effective json.RawMessage, records []StoredWorkflow) (json.RawMessage, error) {
	runnable := make([]StoredWorkflow, 0, len(records))
	for _, record := range records {
		var definition api.WorkflowSpec
		if len(record.Published) > 0 && json.Unmarshal(record.Published, &definition) == nil && StageWorkflowRunnable(definition) {
			runnable = append(runnable, record)
		}
	}
	return mergeStoredWorkflowDefinitions(effective, runnable)
}
