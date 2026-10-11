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
			if err := validateCompensationSourceTx(ctx, tx, op, report); err != nil {
				return err
			}
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
	if err := validateCompensationSourceTx(ctx, tx, op, report); err != nil {
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

func (s *PgStore) ValidateOperationWorkflowStates(ctx context.Context, id string, authority OperationExecutionAuthority, reports []api.OperationWorkflowStateReport, milestones []api.OperationMilestoneRequest) error {
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
	for _, fact := range milestones {
		if err := validateCompensationSourceTx(ctx, tx, op, fact); err != nil {
			return err
		}
	}
	if err := validateOperationWorkflowStateBatch(op, def, reports, milestones); err != nil {
		return err
	}
	q := sqlc.New()
	operationID, _ := operationUUID(id)
	for _, report := range reports {
		canonical, fingerprint, err := canonicalOperationWorkflowState(op, def, report)
		if err != nil {
			return err
		}
		for _, evidenceID := range requiredWorkflowEvidenceIDs(def, report) {
			milestoneID, _ := operationUUID(evidenceID)
			reportID, _ := operationUUID(report.ID)
			used, err := q.CustomerOperationWorkflowEvidenceAlreadyUsed(ctx, tx, sqlc.CustomerOperationWorkflowEvidenceAlreadyUsedParams{OperationID: operationID, StateReportID: reportID, MilestoneID: milestoneID})
			if err != nil {
				return err
			}
			if used {
				return fmt.Errorf("%w: required business evidence already used by another report", ErrInvalidArgument)
			}
		}
		stateID, _ := operationUUID(report.ID)
		prior, err := q.GetCustomerOperationWorkflowStateReport(ctx, tx, sqlc.GetCustomerOperationWorkflowStateReportParams{OperationID: operationID, ID: stateID})
		if errors.Is(err, pgx.ErrNoRows) {
			if err := validateWorkflowResolutionSourcesTx(ctx, tx, op, canonical); err != nil {
				return err
			}
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
	payloads := map[string][]byte{}
	for _, evidence := range report.EvidenceMilestones {
		milestoneID, _ := operationUUID(evidence.ID)
		milestone, err := q.GetCustomerOperationMilestone(ctx, tx, sqlc.GetCustomerOperationMilestoneParams{OperationID: operationID, ID: milestoneID})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && milestone.Name != evidence.Name {
			return api.OperationWorkflowStateReportResponse{}, fmt.Errorf("%w: workflow state evidence references an unpublished milestone", ErrInvalidArgument)
		}
		if err != nil {
			return api.OperationWorkflowStateReportResponse{}, err
		}
		payloads[evidence.ID] = milestone.Payload
	}
	if err := validateWorkflowPolicyEvidence(def, report, payloads); err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	for _, evidenceID := range requiredWorkflowEvidenceIDs(def, report) {
		milestoneID, _ := operationUUID(evidenceID)
		reportID, _ := operationUUID(report.ID)
		used, err := q.CustomerOperationWorkflowEvidenceAlreadyUsed(ctx, tx, sqlc.CustomerOperationWorkflowEvidenceAlreadyUsedParams{OperationID: operationID, StateReportID: reportID, MilestoneID: milestoneID})
		if err != nil {
			return api.OperationWorkflowStateReportResponse{}, err
		}
		if used {
			return api.OperationWorkflowStateReportResponse{}, fmt.Errorf("%w: required business evidence already used by another report", ErrInvalidArgument)
		}
	}
	stateID, _ := operationUUID(report.ID)
	prior, err := q.GetCustomerOperationWorkflowStateReport(ctx, tx, sqlc.GetCustomerOperationWorkflowStateReportParams{OperationID: operationID, ID: stateID})
	if err == nil {
		if prior.Fingerprint != fingerprint {
			return api.OperationWorkflowStateReportResponse{}, ErrOperationInputConflict
		}
		return api.OperationWorkflowStateReportResponse{ID: prior.ID, OperationID: id, Workflow: prior.Workflow, InstanceID: prior.InstanceID,
			FromState: prior.FromState, State: prior.State, Revision: prior.Revision,
			Blockers: decodeWorkflowBlockers(prior.Blockers), BlockerResolutions: decodeWorkflowResolutions(prior.BlockerResolutions), DependsOn: decodeWorkflowDependencies(prior.DependsOn), DependenciesOnly: prior.DependenciesOnly, OutcomeCode: prior.OutcomeCode, OutcomeDescription: prior.OutcomeDescription, OutcomeOnly: prior.OutcomeOnly, DeadlineAt: prior.DeadlineAt, DeadlineOnly: prior.DeadlineOnly, BlockersOnly: prior.BlockersOnly, ContractVersion: int(prior.ContractVersion), EvidenceMilestones: decodeWorkflowEvidence(prior.EvidenceMilestones)}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	evidenceJSON, err := json.Marshal(report.EvidenceMilestones)
	if err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	if report.EvidenceMilestones == nil {
		evidenceJSON = []byte("[]")
	}
	if err := validateWorkflowResolutionSourcesTx(ctx, tx, op, report); err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	dependenciesJSON, _ := json.Marshal(report.DependsOn)
	if report.DependsOn == nil {
		dependenciesJSON = []byte("[]")
	}
	resolutionsJSON, _ := json.Marshal(report.BlockerResolutions)
	if report.BlockerResolutions == nil {
		resolutionsJSON = []byte("[]")
	}
	blockersJSON, _ := json.Marshal(report.Blockers)
	if report.Blockers == nil {
		blockersJSON = []byte("[]")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := q.InsertCustomerOperationWorkflowStateReport(ctx, tx, sqlc.InsertCustomerOperationWorkflowStateReportParams{
		OperationID: operationID, ID: stateID, Workflow: report.Workflow, InstanceID: report.InstanceID,
		FromState: report.FromState, State: report.State, Revision: report.Revision, OccurredAt: pgtype.Timestamptz{Time: report.OccurredAt, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, Fingerprint: fingerprint,
		ContractVersion: int32(report.ContractVersion), EvidenceMilestones: evidenceJSON, Blockers: blockersJSON, BlockerResolutions: resolutionsJSON, DependsOn: dependenciesJSON, DependenciesOnly: report.DependenciesOnly, OutcomeCode: report.OutcomeCode, OutcomeDescription: report.OutcomeDescription, OutcomeOnly: report.OutcomeOnly, DeadlineAt: report.DeadlineAt, DeadlineOnly: report.DeadlineOnly, BlockersOnly: report.BlockersOnly,
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
		InstanceID: report.InstanceID, FromState: report.FromState, State: report.State, Revision: report.Revision,
		Blockers: append([]api.OperationWorkflowBlocker(nil), report.Blockers...), BlockerResolutions: append([]api.OperationWorkflowBlockerResolution(nil), report.BlockerResolutions...), DependsOn: append([]api.OperationWorkflowDependency(nil), report.DependsOn...), DependenciesOnly: report.DependenciesOnly, OutcomeCode: report.OutcomeCode, OutcomeDescription: report.OutcomeDescription, OutcomeOnly: report.OutcomeOnly, DeadlineAt: report.DeadlineAt, DeadlineOnly: report.DeadlineOnly, BlockersOnly: report.BlockersOnly, ContractVersion: report.ContractVersion, EvidenceMilestones: append([]api.OperationWorkflowEvidenceMilestone(nil), report.EvidenceMilestones...)}, nil
}

func decodeWorkflowEvidence(raw []byte) []api.OperationWorkflowEvidenceMilestone {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var evidence []api.OperationWorkflowEvidenceMilestone
	if json.Unmarshal(raw, &evidence) != nil {
		return nil
	}
	return evidence
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
	var workflowInstanceState *api.OperationWorkflowState
	if opts.SubjectType != "" {
		var stateRows [][]byte
		if operator {
			stateRows, err = q.ListAccountCustomerOperationWorkflowStatesBySubject(ctx, s.pool, sqlc.ListAccountCustomerOperationWorkflowStatesBySubjectParams{
				AccountID: accountID, AppID: appID, TenantID: opts.TenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID,
				WorkflowName: opts.Workflow, WorkflowInstanceID: opts.WorkflowInstanceID, StaleOnly: opts.WorkflowStaleOnly && opts.Workflow == "", Now: now, PageLimit: statePageLimit,
			})
		} else {
			tenantID, _ := operationUUID(tenant)
			stateRows, err = q.ListPlatformTenantCustomerOperationWorkflowStatesBySubject(ctx, s.pool, sqlc.ListPlatformTenantCustomerOperationWorkflowStatesBySubjectParams{
				AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID,
				WorkflowName: opts.Workflow, WorkflowInstanceID: opts.WorkflowInstanceID, StaleOnly: opts.WorkflowStaleOnly && opts.Workflow == "", Now: now, PageLimit: statePageLimit,
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
			evaluateOperationWorkflowDeadline(now.Time, &state)
			if opts.Workflow != "" && state.Workflow == opts.Workflow && state.InstanceID == opts.WorkflowInstanceID {
				copy := state
				workflowInstanceState = &copy
			}
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
		declarationRows, err := q.ListCustomerOperationWorkflowStepDeclarations(ctx, s.pool, sqlc.ListCustomerOperationWorkflowStepDeclarationsParams{
			AccountID: accountID, AppID: appID, Scope: opts.Scope, WorkflowName: opts.Workflow,
		})
		if err != nil {
			return api.OperationMilestonesResponse{}, fmt.Errorf("state: read workflow step declarations: %w", mapErr(err))
		}
		declarations := make([]operationWorkflowStepDeclaration, 0, len(declarationRows))
		for _, row := range declarationRows {
			var spec api.OperationWorkflowSpec
			if err := json.Unmarshal([]byte(row.DeclaredStep), &spec); err != nil {
				return api.OperationMilestonesResponse{}, fmt.Errorf("state: decode workflow step declaration: %w", err)
			}
			declarations = append(declarations, operationWorkflowStepDeclaration{Operation: row.Name, Spec: spec, Active: row.Active.Valid && row.Active.Bool})
		}
		contractVersion := int32(0)
		if workflowInstanceState != nil {
			contractVersion = int32(effectiveWorkflowContractVersion(workflowInstanceState.ContractVersion))
		} else if n := len(page.WorkflowStateHistory); n > 0 {
			contractVersion = int32(effectiveWorkflowContractVersion(page.WorkflowStateHistory[n-1].ContractVersion))
		}
		if operator {
			accountRows, err := q.ListAccountCustomerOperationWorkflowStepFactsBySubject(ctx, s.pool, sqlc.ListAccountCustomerOperationWorkflowStepFactsBySubjectParams{
				AccountID: accountID, AppID: appID, TenantID: opts.TenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID,
				WorkflowName: opts.Workflow, WorkflowInstanceID: opts.WorkflowInstanceID, Now: now, ContractVersion: contractVersion,
			})
			if err != nil {
				return api.OperationMilestonesResponse{}, fmt.Errorf("state: read retained workflow step facts: %w", mapErr(err))
			}
			retainedFacts := make([]operationWorkflowStepFactSummary, 0, len(accountRows))
			for _, row := range accountRows {
				retainedFacts = append(retainedFacts, operationWorkflowStepFactSummary{ContractVersion: int(row.ContractVersion), Step: row.Step,
					Label: row.Label, Operation: row.Operation, Milestone: row.Milestone, Position: int(row.Position),
					MilestonesInRetention: row.MilestonesInRetention, Latest: api.OperationWorkflowInstanceMilestoneRef{ID: row.ID,
						OperationID: row.OperationID, OccurredAt: row.OccurredAt.Time, PublishedAt: row.CreatedAt.Time}})
			}
			page = projectOperationWorkflowInstance(page, opts.Workflow, opts.WorkflowInstanceID, declarations, workflowInstanceState, retainedFacts)
		} else {
			tenantID, _ := operationUUID(tenant)
			retainedFactRows, err := q.ListPlatformTenantCustomerOperationWorkflowStepFactsBySubject(ctx, s.pool, sqlc.ListPlatformTenantCustomerOperationWorkflowStepFactsBySubjectParams{
				AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID,
				WorkflowName: opts.Workflow, WorkflowInstanceID: opts.WorkflowInstanceID, Now: now, ContractVersion: contractVersion,
			})
			if err != nil {
				return api.OperationMilestonesResponse{}, fmt.Errorf("state: read retained workflow step facts: %w", mapErr(err))
			}
			retainedFacts := make([]operationWorkflowStepFactSummary, 0, len(retainedFactRows))
			for _, row := range retainedFactRows {
				retainedFacts = append(retainedFacts, operationWorkflowStepFactSummary{ContractVersion: int(row.ContractVersion), Step: row.Step,
					Label: row.Label, Operation: row.Operation, Milestone: row.Milestone, Position: int(row.Position),
					MilestonesInRetention: row.MilestonesInRetention, Latest: api.OperationWorkflowInstanceMilestoneRef{ID: row.ID,
						OperationID: row.OperationID, OccurredAt: row.OccurredAt.Time, PublishedAt: row.CreatedAt.Time}})
			}
			page = projectOperationWorkflowInstance(page, opts.Workflow, opts.WorkflowInstanceID, declarations, workflowInstanceState, retainedFacts)
		}
	}
	if err := s.projectResolutionVerifications(ctx, &page, account, tenant, opts, now.Time); err != nil {
		return api.OperationMilestonesResponse{}, err
	}
	if err := s.projectRelatedWorkflows(ctx, &page, account, tenant, opts, operator, now.Time); err != nil {
		return api.OperationMilestonesResponse{}, err
	}
	if !opts.ReadinessOnly {
		if err := s.projectDependencyImpact(ctx, &page, account, tenant, opts, operator, now.Time); err != nil {
			return api.OperationMilestonesResponse{}, err
		}
		if err := s.projectDependencyTrace(ctx, &page, account, tenant, opts, operator, now.Time); err != nil {
			return api.OperationMilestonesResponse{}, err
		}
		projectOperationWorkflowReadiness(&page)
	}
	return page, nil
}

func decodeWorkflowBlockers(raw []byte) []api.OperationWorkflowBlocker {
	var blockers []api.OperationWorkflowBlocker
	_ = json.Unmarshal(raw, &blockers)
	return blockers
}

func decodeWorkflowResolutions(raw []byte) []api.OperationWorkflowBlockerResolution {
	var resolutions []api.OperationWorkflowBlockerResolution
	_ = json.Unmarshal(raw, &resolutions)
	return resolutions
}
func validateWorkflowResolutionSourcesTx(ctx context.Context, tx pgx.Tx, op Operation, report api.OperationWorkflowStateReport) error {
	accountID, _ := operationUUID(op.AccountID)
	appID, _ := operationUUID(op.AppID)
	tenantID, _ := operationUUID(op.PlatformTenantID)
	for _, resolution := range report.BlockerResolutions {
		if op.Subject == nil {
			return ErrInvalidArgument
		}
		sourceOperationID, _ := operationUUID(resolution.BlockerOperationID)
		sourceReportID, _ := operationUUID(resolution.BlockerReportID)
		raw, err := sqlc.New().GetCustomerOperationWorkflowResolutionSource(ctx, tx, sqlc.GetCustomerOperationWorkflowResolutionSourceParams{
			SourceOperationID: sourceOperationID, SourceReportID: sourceReportID, AccountID: accountID, AppID: appID, TenantID: tenantID,
			Scope: op.Scope, SubjectType: op.Subject.Type, SubjectID: op.Subject.ID, WorkflowName: report.Workflow, InstanceID: report.InstanceID,
			ContractVersion: int32(report.ContractVersion), SourceRevision: resolution.BlockerRevision, Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: workflow resolution source report is not retained in this workflow and owner boundary", ErrInvalidArgument)
		}
		if err != nil {
			return err
		}
		if !workflowResolutionBlockerExists(decodeWorkflowBlockers(raw), resolution) {
			return fmt.Errorf("%w: referenced report did not contain this blocker", ErrInvalidArgument)
		}
	}
	return nil
}
