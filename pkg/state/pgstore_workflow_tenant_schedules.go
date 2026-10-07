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

func (s *PgStore) ListTenantWorkflowScheduleCandidates(ctx context.Context, owner, afterApp, afterTenant string, limit int) ([]WorkflowScheduleCandidate, error) {
	params := sqlc.ListTenantWorkflowScheduleCandidatesParams{BatchLimit: int32(limit)}
	if owner != "" {
		params.OwnerNodeID = mustPgUUID(owner)
	}
	if afterApp != "" {
		params.AfterAppID = mustPgUUID(afterApp)
		params.AfterTenantID = mustPgUUID(afterTenant)
	}
	rows, err := sqlc.New().ListTenantWorkflowScheduleCandidates(ctx, s.pool, params)
	if err != nil {
		return nil, fmt.Errorf("state: list tenant workflow schedules: %w", err)
	}
	result := make([]WorkflowScheduleCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, WorkflowScheduleCandidate{AppID: pgUUIDString(row.AppID),
			PlatformTenantID: pgUUIDString(row.PlatformTenantID), DeploymentID: pgUUIDString(row.DeploymentID),
			Workflows: cloneWorkflowJSON(row.Workflows)})
	}
	return result, nil
}

func tenantWorkflowScheduleCursorFromSQL(row sqlc.PlatformTenantWorkflowScheduleCursor) WorkflowScheduleCursor {
	result := WorkflowScheduleCursor{AppID: pgUUIDString(row.AppID), PlatformTenantID: pgUUIDString(row.PlatformTenantID),
		WorkflowName: row.WorkflowName, TriggerSnapshot: cloneWorkflowJSON(row.TriggerSnapshot),
		LastAdmittedAt: timestamptzToTimePtr(row.LastAdmittedAt), LastEvaluatedAt: timeFromPgtype(row.LastEvaluatedAt), ScheduledFor: timestamptzToTimePtr(row.ScheduledFor), Status: row.Status}
	if row.DeploymentID.Valid {
		result.DeploymentID = pgUUIDString(row.DeploymentID)
	}
	if row.LastRunID.Valid {
		result.LastRunID = pgUUIDString(row.LastRunID)
	}
	return result
}

func (s *PgStore) AdmitTenantScheduledWorkflow(ctx context.Context, appID, tenantID, deploymentID, name string, now time.Time) (WorkflowScheduleCursor, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: begin tenant scheduled workflow: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New()
	if err := queries.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: lock tenant scheduled workflow: %w", err)
	}
	target, err := queries.LockTenantWorkflowScheduleTarget(ctx, tx, sqlc.LockTenantWorkflowScheduleTargetParams{
		TenantID: mustPgUUID(tenantID), AppID: mustPgUUID(appID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowScheduleCursor{}, false, nil
	}
	if err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: resolve tenant scheduled workflow target: %w", err)
	}
	if pgUUIDString(target.DeploymentID) != deploymentID {
		return WorkflowScheduleCursor{}, false, nil
	}
	linkParams := sqlc.LockTenantWorkflowScheduleConsumerLinkParams{AccountID: target.AccountID,
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID)}
	_, consumerErr := queries.LockTenantWorkflowScheduleConsumerLink(ctx, tx, linkParams)
	if consumerErr != nil && !errors.Is(consumerErr, pgx.ErrNoRows) {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: lock tenant consumer schedule link: %w", consumerErr)
	}
	if errors.Is(consumerErr, pgx.ErrNoRows) {
		_, surfaceErr := queries.LockTenantWorkflowScheduleSurfaceLink(ctx, tx, sqlc.LockTenantWorkflowScheduleSurfaceLinkParams{
			AccountID: target.AccountID, AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID),
		})
		if errors.Is(surfaceErr, pgx.ErrNoRows) {
			return WorkflowScheduleCursor{}, false, nil
		}
		if surfaceErr != nil {
			return WorkflowScheduleCursor{}, false, fmt.Errorf("state: lock tenant surface schedule link: %w", surfaceErr)
		}
	}
	plan := api.Plan(target.Plan)
	if !plan.WorkflowsAllowed() {
		return WorkflowScheduleCursor{}, false, nil
	}
	definition, err := scheduledWorkflowDefinition(target.Workflows, name, plan)
	if err != nil || definition == nil {
		return WorkflowScheduleCursor{}, false, err
	}
	storedCursor, err := queries.GetTenantWorkflowScheduleCursor(ctx, tx, sqlc.GetTenantWorkflowScheduleCursorParams{
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID), WorkflowName: name,
	})
	var previous *WorkflowScheduleCursor
	if err == nil {
		value := tenantWorkflowScheduleCursorFromSQL(storedCursor)
		previous = &value
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: get tenant workflow schedule cursor: %w", err)
	}
	var configVersion int64
	customized := false
	if previous != nil {
		configured, version, exists := tenantWorkflowScheduleConfigFromSnapshot(previous.TriggerSnapshot)
		if exists && definition.Trigger.TenantConfigurable {
			effectiveTrigger := applyTenantWorkflowScheduleTrigger(*definition.Trigger, configured)
			if effectiveTrigger.Enabled != nil && !*effectiveTrigger.Enabled {
				return WorkflowScheduleCursor{}, false, nil
			}
			definition.Trigger = &effectiveTrigger
			configVersion, customized = version, true
			previous.TriggerSnapshot, err = json.Marshal(configured)
			if err != nil {
				return WorkflowScheduleCursor{}, false, err
			}
		}
	}
	active, err := queries.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{
		AppID: mustPgUUID(appID), WorkflowName: "",
	})
	if err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	named, err := queries.CountActiveTenantWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveTenantWorkflowRunsForAdmissionParams{
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID), WorkflowName: name,
	})
	if err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	next, run, err := evaluateWorkflowSchedule(appID, tenantID, deploymentID, *definition, previous, now,
		int(active), int(named), plan.WorkflowMaxConcurrentRuns())
	if err != nil || next == nil {
		return WorkflowScheduleCursor{}, false, err
	}
	outcomeChanged := workflowScheduleOutcomeChanged(next, previous)
	if customized {
		next.TriggerSnapshot, err = encodeTenantWorkflowScheduleSnapshot(*definition.Trigger, configVersion)
		if err != nil {
			return WorkflowScheduleCursor{}, false, err
		}
	}
	if run != nil {
		_, err = queries.InsertTenantScheduledWorkflowRun(ctx, tx, sqlc.InsertTenantScheduledWorkflowRunParams{
			DeploymentID: mustPgUUID(run.DeploymentID), ID: mustPgUUID(run.ID), AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID), WorkflowName: name,
			Input: run.Input, DefinitionSnapshot: run.DefinitionSnapshot,
			ScheduledFor: pgtype.Timestamptz{Time: run.ScheduledFor, Valid: true},
		})
		if err != nil {
			return WorkflowScheduleCursor{}, false, fmt.Errorf("state: insert tenant scheduled workflow run: %w", err)
		}
	}
	_, err = queries.UpsertTenantWorkflowScheduleCursor(ctx, tx, sqlc.UpsertTenantWorkflowScheduleCursorParams{
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID), WorkflowName: name,
		DeploymentID: mustPgUUID(deploymentID), TriggerSnapshot: next.TriggerSnapshot,
		LastAdmittedAt: nullableTimestamptzPtr(next.LastAdmittedAt), LastEvaluatedAt: pgtype.Timestamptz{Time: next.LastEvaluatedAt, Valid: true},
		ScheduledFor: nullableTimestamptzPtr(next.ScheduledFor), Status: next.Status, LastRunID: mustPgUUID(next.LastRunID),
	})
	if err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: record tenant workflow schedule outcome: %w", err)
	}
	if err := insertWorkflowScheduleOccurrence(ctx, tx, next, previous, *definition); err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: commit tenant scheduled workflow: %w", err)
	}
	return *next, outcomeChanged, nil
}

func lockTenantWorkflowScheduleBinding(ctx context.Context, tx pgx.Tx, accountID, tenantID, appID string) (sqlc.LockTenantWorkflowScheduleTargetRow, bool, error) {
	queries := sqlc.New()
	target, err := queries.LockTenantWorkflowScheduleTarget(ctx, tx, sqlc.LockTenantWorkflowScheduleTargetParams{
		TenantID: mustPgUUID(tenantID), AppID: mustPgUUID(appID),
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && pgUUIDString(target.AccountID) != accountID {
		return sqlc.LockTenantWorkflowScheduleTargetRow{}, false, nil
	}
	if err != nil {
		return sqlc.LockTenantWorkflowScheduleTargetRow{}, false, fmt.Errorf("state: resolve tenant workflow schedule target: %w", err)
	}
	linkParams := sqlc.LockTenantWorkflowScheduleConsumerLinkParams{AccountID: target.AccountID,
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID)}
	_, consumerErr := queries.LockTenantWorkflowScheduleConsumerLink(ctx, tx, linkParams)
	if consumerErr != nil && !errors.Is(consumerErr, pgx.ErrNoRows) {
		return sqlc.LockTenantWorkflowScheduleTargetRow{}, false, fmt.Errorf("state: lock tenant consumer schedule link: %w", consumerErr)
	}
	if errors.Is(consumerErr, pgx.ErrNoRows) {
		_, surfaceErr := queries.LockTenantWorkflowScheduleSurfaceLink(ctx, tx, sqlc.LockTenantWorkflowScheduleSurfaceLinkParams{
			AccountID: target.AccountID, AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID),
		})
		if errors.Is(surfaceErr, pgx.ErrNoRows) {
			return sqlc.LockTenantWorkflowScheduleTargetRow{}, false, nil
		}
		if surfaceErr != nil {
			return sqlc.LockTenantWorkflowScheduleTargetRow{}, false, fmt.Errorf("state: lock tenant surface schedule link: %w", surfaceErr)
		}
	}
	return target, true, nil
}

func (s *PgStore) ListTenantWorkflowSchedules(ctx context.Context, accountID, tenantID, appID string) ([]TenantWorkflowSchedule, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("state: begin tenant workflow schedule list: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New()
	target, eligible, err := lockTenantWorkflowScheduleBinding(ctx, tx, accountID, tenantID, appID)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, ErrNotFound
	}
	plan := api.Plan(target.Plan)
	if !plan.WorkflowsAllowed() {
		return nil, ErrNotFound
	}
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(target.Workflows, &definitions); err != nil {
		return nil, fmt.Errorf("state: decode tenant workflow schedules: %w", err)
	}
	rows, err := queries.ListTenantWorkflowScheduleCursors(ctx, tx, sqlc.ListTenantWorkflowScheduleCursorsParams{
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID),
	})
	if err != nil {
		return nil, fmt.Errorf("state: read tenant workflow schedule settings: %w", err)
	}
	configured := make(map[string]WorkflowScheduleCursor, len(rows))
	for _, row := range rows {
		cursor := tenantWorkflowScheduleCursorFromSQL(row)
		configured[cursor.WorkflowName] = cursor
	}
	result := make([]TenantWorkflowSchedule, 0)
	for _, definition := range definitions {
		if definition.Trigger == nil || definition.Trigger.Type != "schedule" || !definition.Trigger.TenantConfigurable ||
			(definition.Trigger.Enabled != nil && !*definition.Trigger.Enabled) {
			continue
		}
		if _, err := api.ValidateWorkflowDAG(definition, plan); err != nil {
			return nil, err
		}
		cursor, exists := configured[definition.Name]
		var trigger api.WorkflowTriggerSpec
		var version int64
		customized := false
		if exists {
			trigger, version, customized = tenantWorkflowScheduleConfigFromSnapshot(cursor.TriggerSnapshot)
		}
		schedule := tenantWorkflowScheduleFromDefinition(appID, tenantID, pgUUIDString(target.DeploymentID),
			definition, trigger, version, customized)
		if exists {
			schedule.Cursor = &cursor
		}
		result = append(result, schedule)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: commit tenant workflow schedule list: %w", err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].WorkflowName < result[j].WorkflowName })
	return result, nil
}

func (s *PgStore) UpdateTenantWorkflowSchedule(ctx context.Context, accountID, tenantID, appID, name string,
	expectedVersion int64, schedule, timezone, overlap string, enabled bool) (TenantWorkflowSchedule, error) {
	if expectedVersion < 0 {
		return TenantWorkflowSchedule{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TenantWorkflowSchedule{}, fmt.Errorf("state: begin tenant workflow schedule update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New()
	if err := queries.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return TenantWorkflowSchedule{}, fmt.Errorf("state: lock tenant workflow schedule update: %w", err)
	}
	target, eligible, err := lockTenantWorkflowScheduleBinding(ctx, tx, accountID, tenantID, appID)
	if err != nil {
		return TenantWorkflowSchedule{}, err
	}
	if !eligible {
		return TenantWorkflowSchedule{}, ErrNotFound
	}
	plan := api.Plan(target.Plan)
	if !plan.WorkflowsAllowed() {
		return TenantWorkflowSchedule{}, ErrNotFound
	}
	definition, err := tenantConfigurableWorkflowDefinition(target.Workflows, name, plan)
	if err != nil {
		return TenantWorkflowSchedule{}, err
	}
	if definition == nil {
		return TenantWorkflowSchedule{}, ErrNotFound
	}
	storedCursor, err := queries.GetTenantWorkflowScheduleCursor(ctx, tx, sqlc.GetTenantWorkflowScheduleCursorParams{
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID), WorkflowName: name,
	})
	var previous *WorkflowScheduleCursor
	currentVersion := int64(0)
	if err == nil {
		value := tenantWorkflowScheduleCursorFromSQL(storedCursor)
		previous = &value
		_, currentVersion, _ = tenantWorkflowScheduleConfigFromSnapshot(value.TriggerSnapshot)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return TenantWorkflowSchedule{}, fmt.Errorf("state: read tenant workflow schedule version: %w", err)
	}
	if currentVersion != expectedVersion {
		return TenantWorkflowSchedule{}, ErrConflict
	}
	trigger, err := normalizeTenantWorkflowScheduleTrigger(*definition.Trigger, schedule, timezone, overlap, enabled)
	if err != nil {
		return TenantWorkflowSchedule{}, err
	}
	version := currentVersion + 1
	snapshot, err := encodeTenantWorkflowScheduleSnapshot(trigger, version)
	if err != nil {
		return TenantWorkflowSchedule{}, err
	}
	now := time.Now().UTC()
	next := WorkflowScheduleCursor{AppID: appID, PlatformTenantID: tenantID, WorkflowName: name,
		DeploymentID: pgUUIDString(target.DeploymentID), TriggerSnapshot: snapshot, LastEvaluatedAt: now,
		Status: WorkflowScheduleArmed}
	if previous != nil {
		next.ScheduledFor = previous.ScheduledFor
		next.LastRunID = previous.LastRunID
		next.LastAdmittedAt = cloneTimePtr(previous.LastAdmittedAt)
		if next.ScheduledFor != nil && next.ScheduledFor.After(now) {
			next.ScheduledFor = nil
		}
	}
	_, err = queries.UpsertTenantWorkflowScheduleCursor(ctx, tx, sqlc.UpsertTenantWorkflowScheduleCursorParams{
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID), WorkflowName: name,
		DeploymentID: target.DeploymentID, TriggerSnapshot: next.TriggerSnapshot,
		LastAdmittedAt: nullableTimestamptzPtr(next.LastAdmittedAt), LastEvaluatedAt: pgtype.Timestamptz{Time: next.LastEvaluatedAt, Valid: true},
		ScheduledFor: nullableTimestamptzPtr(next.ScheduledFor), Status: next.Status, LastRunID: mustPgUUID(next.LastRunID),
	})
	if err != nil {
		return TenantWorkflowSchedule{}, fmt.Errorf("state: save tenant workflow schedule: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantWorkflowSchedule{}, fmt.Errorf("state: commit tenant workflow schedule update: %w", err)
	}
	return tenantWorkflowScheduleFromDefinition(appID, tenantID, next.DeploymentID, *definition, trigger, version, true), nil
}

var _ TenantWorkflowScheduleStore = (*PgStore)(nil)
