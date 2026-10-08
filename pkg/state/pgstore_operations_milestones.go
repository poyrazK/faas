// ADR-715: milestone identity outlives the publishing execution claim.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func milestoneAuthorityTx(ctx context.Context, tx pgx.Tx, id string, authority OperationExecutionAuthority) (Operation, Invocation, OperationDefinition, error) {
	inv, err := operationLockedInvocation(ctx, tx, authority.InvocationID)
	if err != nil {
		return Operation{}, inv, OperationDefinition{}, mapErr(err)
	}
	op, def, _, exists, err := operationForInvocationTx(ctx, tx, inv.ID)
	if err != nil {
		return op, inv, def, err
	}
	if !exists || op.ID != id {
		return op, inv, def, ErrNotFound
	}
	if err := ValidateOperationExecutionAuthority(op, inv, authority, time.Now().UTC()); err != nil {
		return op, inv, def, err
	}
	account, _ := operationUUID(op.AccountID)
	tenant, _ := operationUUID(op.PlatformTenantID)
	status, err := sqlc.New().LockCustomerOperationTenant(ctx, tx, sqlc.LockCustomerOperationTenantParams{AccountID: account, TenantID: tenant})
	if err != nil {
		return op, inv, def, mapErr(err)
	}
	if status != PlatformTenantActive {
		return op, inv, def, ErrPlatformTenantSuspended
	}
	return op, inv, def, nil
}

func milestonePGRecord(row sqlc.GetCustomerOperationMilestoneRow, op Operation, def OperationDefinition) (api.OperationMilestone, error) {
	result := api.OperationMilestone{ID: row.ID, OperationID: row.OperationID, Name: row.Name, Payload: row.Payload, OccurredAt: row.OccurredAt.Time, CreatedAt: row.CreatedAt.Time, Sequence: row.EventSequence}
	var err error
	result.WorkflowSteps, err = operations.WorkflowStepsForMilestone(def.Spec, row.Name, row.Payload)
	if err != nil {
		return api.OperationMilestone{}, fmt.Errorf("state: resolve retained workflow instance: %w", err)
	}
	if op.Subject != nil {
		subject := *op.Subject
		result.Subject = &subject
	}
	return result, nil
}

func (s *PgStore) ValidateOperationMilestones(ctx context.Context, id string, authority OperationExecutionAuthority, reports []api.OperationMilestoneRequest) error {
	if _, err := operationUUID(authority.InvocationID); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	op, _, def, err := milestoneAuthorityTx(ctx, tx, id, authority)
	if err != nil {
		return err
	}
	if err := validateOperationMilestoneBatch(op, def, reports); err != nil {
		return err
	}
	q := sqlc.New()
	operationID, _ := operationUUID(id)
	fresh := 0
	for _, report := range reports {
		_, fingerprint, err := canonicalOperationMilestone(op, def, report)
		if err != nil {
			return err
		}
		milestoneID, _ := operationUUID(report.ID)
		prior, err := q.GetCustomerOperationMilestone(ctx, tx, sqlc.GetCustomerOperationMilestoneParams{OperationID: operationID, ID: milestoneID})
		if errors.Is(err, pgx.ErrNoRows) {
			fresh++
			continue
		}
		if err != nil {
			return err
		}
		if prior.Fingerprint != fingerprint {
			return ErrOperationInputConflict
		}
	}
	if op.MilestoneCount+fresh > api.OperationMilestonesMaxPerOperation {
		return NewOperationLimitError("milestones_per_operation", api.OperationMilestonesMaxPerOperation, int64(op.MilestoneCount+fresh))
	}
	return nil
}

func (s *PgStore) ReportOperationMilestone(ctx context.Context, id string, authority OperationExecutionAuthority, report api.OperationMilestoneRequest) (api.OperationMilestone, error) {
	if _, err := operationUUID(authority.InvocationID); err != nil {
		return api.OperationMilestone{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.OperationMilestone{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	op, inv, def, err := milestoneAuthorityTx(ctx, tx, id, authority)
	if err != nil {
		return api.OperationMilestone{}, err
	}
	report, fingerprint, err := canonicalOperationMilestone(op, def, report)
	if err != nil {
		return api.OperationMilestone{}, err
	}
	q := sqlc.New()
	operationID, _ := operationUUID(id)
	milestoneID, _ := operationUUID(report.ID)
	prior, err := q.GetCustomerOperationMilestone(ctx, tx, sqlc.GetCustomerOperationMilestoneParams{OperationID: operationID, ID: milestoneID})
	if err == nil {
		if prior.Fingerprint != fingerprint {
			return api.OperationMilestone{}, ErrOperationInputConflict
		}
		return milestonePGRecord(prior, op, def)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.OperationMilestone{}, err
	}
	milestone, event, err := newOperationMilestone(&op, inv, def, report, time.Now().UTC())
	if err != nil {
		return milestone, err
	}
	if err := q.InsertCustomerOperationMilestone(ctx, tx, sqlc.InsertCustomerOperationMilestoneParams{OperationID: operationID, ID: milestoneID, EventSequence: milestone.Sequence, Name: milestone.Name, Payload: milestone.Payload, OccurredAt: pgtype.Timestamptz{Time: milestone.OccurredAt, Valid: true}, CreatedAt: pgtype.Timestamptz{Time: milestone.CreatedAt, Valid: true}, Fingerprint: fingerprint}); err != nil {
		return api.OperationMilestone{}, err
	}
	if err := operationSaveTx(ctx, tx, op, event); err != nil {
		return api.OperationMilestone{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.OperationMilestone{}, err
	}
	return milestone, nil
}

func (s *PgStore) ValidateOperationWorkflowStates(ctx context.Context, id string, authority OperationExecutionAuthority, reports []api.OperationWorkflowStateReport) error {
	if _, err := operationUUID(authority.InvocationID); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	op, _, def, err := milestoneAuthorityTx(ctx, tx, id, authority)
	if err != nil {
		return err
	}
	if err := validateOperationWorkflowStateBatch(op, def, reports); err != nil {
		return err
	}
	q := sqlc.New()
	operationID, _ := operationUUID(id)
	for _, report := range reports {
		_, fingerprint, err := canonicalOperationWorkflowState(op, def, report)
		if err != nil {
			return err
		}
		stateID, _ := operationUUID(report.ID)
		prior, err := q.GetCustomerOperationWorkflowStateReport(ctx, tx, sqlc.GetCustomerOperationWorkflowStateReportParams{OperationID: operationID, ID: stateID})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if prior.Fingerprint != fingerprint {
			return ErrOperationInputConflict
		}
	}
	return nil
}

func (s *PgStore) ReportOperationWorkflowState(ctx context.Context, id string, authority OperationExecutionAuthority, report api.OperationWorkflowStateReport) (api.OperationWorkflowStateReportResponse, error) {
	if _, err := operationUUID(authority.InvocationID); err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	op, _, def, err := milestoneAuthorityTx(ctx, tx, id, authority)
	if err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	report, fingerprint, err := canonicalOperationWorkflowState(op, def, report)
	if err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	q := sqlc.New()
	operationID, _ := operationUUID(id)
	stateID, _ := operationUUID(report.ID)
	prior, err := q.GetCustomerOperationWorkflowStateReport(ctx, tx, sqlc.GetCustomerOperationWorkflowStateReportParams{OperationID: operationID, ID: stateID})
	if err == nil {
		if prior.Fingerprint != fingerprint {
			return api.OperationWorkflowStateReportResponse{}, ErrOperationInputConflict
		}
		return api.OperationWorkflowStateReportResponse{ID: prior.ID, OperationID: id, Workflow: prior.Workflow, InstanceID: prior.InstanceID,
			FromState: prior.FromState, State: prior.State, Revision: prior.Revision}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := q.InsertCustomerOperationWorkflowStateReport(ctx, tx, sqlc.InsertCustomerOperationWorkflowStateReportParams{
		OperationID: operationID, ID: stateID, Workflow: report.Workflow, InstanceID: report.InstanceID,
		FromState: report.FromState, State: report.State, Revision: report.Revision, OccurredAt: pgtype.Timestamptz{Time: report.OccurredAt, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, Fingerprint: fingerprint,
	}); err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	if err := q.UpsertCustomerOperationWorkflowState(ctx, tx, sqlc.UpsertCustomerOperationWorkflowStateParams{
		Workflow: report.Workflow, InstanceID: report.InstanceID, State: report.State, Revision: report.Revision,
		ReportID: stateID, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}, OperationID: operationID,
	}); err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	return api.OperationWorkflowStateReportResponse{ID: report.ID, OperationID: id, Workflow: report.Workflow,
		InstanceID: report.InstanceID, FromState: report.FromState, State: report.State, Revision: report.Revision}, nil
}

func (s *PgStore) ListPlatformTenantOperationMilestones(ctx context.Context, account, tenant string, opts api.OperationMilestoneListOptions) (api.OperationMilestonesResponse, error) {
	return s.listOperationMilestones(ctx, account, tenant, opts, false)
}
func (s *PgStore) ListAccountOperationMilestones(ctx context.Context, account string, opts api.OperationMilestoneListOptions) (api.OperationMilestonesResponse, error) {
	return s.listOperationMilestones(ctx, account, opts.TenantID, opts, true)
}

func (s *PgStore) listOperationMilestones(ctx context.Context, account, tenant string, opts api.OperationMilestoneListOptions, operator bool) (api.OperationMilestonesResponse, error) {
	opts, cursor, err := prepareOperationMilestoneHistory(account, tenant, opts, operator)
	if err != nil {
		return api.OperationMilestonesResponse{}, err
	}
	stateHistoryCursor, err := prepareOperationWorkflowStateHistoryCursor(account, tenant, opts, operator)
	if err != nil {
		return api.OperationMilestonesResponse{}, err
	}
	if opts.Limit < 1 || opts.Limit > api.OperationHistoryPageMax {
		return api.OperationMilestonesResponse{}, ErrInvalidArgument
	}
	accountID, _ := operationUUID(account)
	appID, _ := operationUUID(opts.AppID)
	operationID, _ := operationUUID(opts.OperationID)
	beforeOperation, _ := operationUUID(cursor.OperationID)
	beforeID, _ := operationUUID(cursor.ID)
	var beforeCreated pgtype.Timestamptz
	if cursor.ID != "" {
		beforeCreated = pgtype.Timestamptz{Time: cursor.CreatedAt, Valid: true}
	}
	now := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	statePageLimit := int32(opts.Limit)
	limit := statePageLimit + 1
	q := sqlc.New()
	var raw [][]byte
	if operator {
		if opts.SubjectType != "" {
			raw, err = q.ListAccountCustomerOperationMilestonesBySubject(ctx, s.pool, sqlc.ListAccountCustomerOperationMilestonesBySubjectParams{AccountID: accountID, AppID: appID, TenantID: opts.TenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, WorkflowName: opts.Workflow, WorkflowInstanceID: opts.WorkflowInstanceID, Now: now, BeforeCreatedAt: beforeCreated, BeforeOperationID: beforeOperation, BeforeID: beforeID, PageLimit: limit})
		} else {
			raw, err = q.ListAccountCustomerOperationMilestones(ctx, s.pool, sqlc.ListAccountCustomerOperationMilestonesParams{AccountID: accountID, AppID: appID, TenantID: opts.TenantID, Scope: opts.Scope, OperationID: operationID, Now: now, BeforeCreatedAt: beforeCreated, BeforeOperationID: beforeOperation, BeforeID: beforeID, PageLimit: limit})
		}
	} else {
		tenantID, _ := operationUUID(tenant)
		if opts.SubjectType != "" {
			raw, err = q.ListPlatformTenantCustomerOperationMilestonesBySubject(ctx, s.pool, sqlc.ListPlatformTenantCustomerOperationMilestonesBySubjectParams{AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, WorkflowName: opts.Workflow, WorkflowInstanceID: opts.WorkflowInstanceID, Now: now, BeforeCreatedAt: beforeCreated, BeforeOperationID: beforeOperation, BeforeID: beforeID, PageLimit: limit})
		} else {
			raw, err = q.ListPlatformTenantCustomerOperationMilestones(ctx, s.pool, sqlc.ListPlatformTenantCustomerOperationMilestonesParams{AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, OperationID: operationID, Now: now, BeforeCreatedAt: beforeCreated, BeforeOperationID: beforeOperation, BeforeID: beforeID, PageLimit: limit})
		}
	}
	if err != nil {
		return api.OperationMilestonesResponse{}, fmt.Errorf("state: read milestones: %w", mapErr(err))
	}
	rows := make([]api.OperationMilestone, 0, len(raw))
	for _, data := range raw {
		var row api.OperationMilestone
		if err := json.Unmarshal(data, &row); err != nil {
			return api.OperationMilestonesResponse{}, fmt.Errorf("state: decode milestone: %w", err)
		}
		row.WorkflowSteps, err = operations.ResolveWorkflowInstanceIDs(row.WorkflowSteps, row.Payload)
		if err != nil {
			return api.OperationMilestonesResponse{}, fmt.Errorf("state: resolve retained workflow instance: %w", err)
		}
		if opts.Workflow != "" {
			row.WorkflowSteps = filterOperationWorkflowSteps(row.WorkflowSteps, opts.Workflow, opts.WorkflowInstanceID)
		}
		rows = append(rows, row)
	}
	page := operationMilestonePage(rows, opts.Limit, cursor)
	if opts.SubjectType != "" {
		var stateRows [][]byte
		if operator {
			stateRows, err = q.ListAccountCustomerOperationWorkflowStatesBySubject(ctx, s.pool, sqlc.ListAccountCustomerOperationWorkflowStatesBySubjectParams{
				AccountID: accountID, AppID: appID, TenantID: opts.TenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID,
				WorkflowName: opts.Workflow, WorkflowInstanceID: opts.WorkflowInstanceID, StaleOnly: opts.WorkflowStaleOnly, Now: now, PageLimit: statePageLimit,
			})
		} else {
			tenantID, _ := operationUUID(tenant)
			stateRows, err = q.ListPlatformTenantCustomerOperationWorkflowStatesBySubject(ctx, s.pool, sqlc.ListPlatformTenantCustomerOperationWorkflowStatesBySubjectParams{
				AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID,
				WorkflowName: opts.Workflow, WorkflowInstanceID: opts.WorkflowInstanceID, StaleOnly: opts.WorkflowStaleOnly, Now: now, PageLimit: statePageLimit,
			})
		}
		if err != nil {
			return api.OperationMilestonesResponse{}, fmt.Errorf("state: read workflow states: %w", mapErr(err))
		}
		page.WorkflowStates = make([]api.OperationWorkflowState, 0, len(stateRows))
		for _, data := range stateRows {
			var state api.OperationWorkflowState
			if err := json.Unmarshal(data, &state); err != nil {
				return api.OperationMilestonesResponse{}, fmt.Errorf("state: decode workflow state: %w", err)
			}
			state.Stale = operationWorkflowStateIsStale(now.Time, state)
			if opts.WorkflowStaleOnly && !state.Stale {
				continue
			}
			page.WorkflowStates = append(page.WorkflowStates, state)
		}
	}
	if opts.Workflow != "" {
		var afterRevision pgtype.Int8
		var afterPublishedAt pgtype.Timestamptz
		afterOperationID, _ := operationUUID(stateHistoryCursor.OperationID)
		afterID, _ := operationUUID(stateHistoryCursor.ID)
		if stateHistoryCursor.ID != "" {
			afterRevision = pgtype.Int8{Int64: stateHistoryCursor.Revision, Valid: true}
			afterPublishedAt = pgtype.Timestamptz{Time: stateHistoryCursor.PublishedAt, Valid: true}
		}
		var historyRows [][]byte
		if operator {
			historyRows, err = q.ListAccountCustomerOperationWorkflowStateHistory(ctx, s.pool, sqlc.ListAccountCustomerOperationWorkflowStateHistoryParams{
				AccountID: accountID, AppID: appID, TenantID: opts.TenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID,
				WorkflowName: opts.Workflow, WorkflowInstanceID: opts.WorkflowInstanceID, Now: now,
				AfterRevision: afterRevision, AfterPublishedAt: afterPublishedAt, AfterOperationID: afterOperationID, AfterID: afterID, PageLimit: limit,
			})
		} else {
			tenantID, _ := operationUUID(tenant)
			historyRows, err = q.ListPlatformTenantCustomerOperationWorkflowStateHistory(ctx, s.pool, sqlc.ListPlatformTenantCustomerOperationWorkflowStateHistoryParams{
				AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID,
				WorkflowName: opts.Workflow, WorkflowInstanceID: opts.WorkflowInstanceID, Now: now,
				AfterRevision: afterRevision, AfterPublishedAt: afterPublishedAt, AfterOperationID: afterOperationID, AfterID: afterID, PageLimit: limit,
			})
		}
		if err != nil {
			return api.OperationMilestonesResponse{}, fmt.Errorf("state: read workflow state history: %w", mapErr(err))
		}
		entries := make([]api.OperationWorkflowStateHistoryEntry, 0, len(historyRows))
		for _, data := range historyRows {
			var entry api.OperationWorkflowStateHistoryEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				return api.OperationMilestonesResponse{}, fmt.Errorf("state: decode workflow state history: %w", err)
			}
			entries = append(entries, entry)
		}
		page.WorkflowStateHistory, page.NextWorkflowStateCursor = operationWorkflowStateHistoryPage(entries, opts.Limit, stateHistoryCursor)
	}
	return page, nil
}
