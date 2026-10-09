// ADR-715: publication receipts belong to logical work, not its reporting attempt.
package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func (m *MemStore) milestoneAuthorityLocked(id string, authority OperationExecutionAuthority) (Operation, Invocation, OperationDefinition, error) {
	data := m.operationMemoryLocked()
	op, exists := data.operations[id]
	if !exists {
		return op, Invocation{}, OperationDefinition{}, ErrNotFound
	}
	inv, exists := m.invocations[authority.InvocationID]
	if !exists {
		return op, inv, OperationDefinition{}, ErrNotFound
	}
	if err := ValidateOperationExecutionAuthority(op, inv, authority, time.Now().UTC()); err != nil {
		return op, inv, OperationDefinition{}, err
	}
	if err := m.platformTenantInvocationAllowedLocked(inv); err != nil {
		return op, inv, OperationDefinition{}, err
	}
	return cloneOperation(op), inv, data.definitions[op.DefinitionID], nil
}

func (m *MemStore) ValidateOperationWorkflowStates(_ context.Context, id string, authority OperationExecutionAuthority, reports []api.OperationWorkflowStateReport, milestones []api.OperationMilestoneRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, def, err := m.milestoneAuthorityLocked(id, authority)
	if err != nil {
		return err
	}
	for _, fact := range milestones {
		if err := m.validateCompensationSourceLocked(op, fact); err != nil {
			return err
		}
	}
	if err := validateOperationWorkflowStateBatch(op, def, reports, milestones); err != nil {
		return err
	}
	data := m.operationMemoryLocked()
	for _, report := range reports {
		canonical, fingerprint, err := canonicalOperationWorkflowState(op, def, report)
		if err != nil {
			return err
		}
		for _, evidenceID := range requiredWorkflowEvidenceIDs(def, report) {
			for _, prior := range data.workflowStateReports {
				if prior.History.OperationID != id || prior.History.ID == report.ID {
					continue
				}
				for _, used := range prior.History.EvidenceMilestones {
					if used.ID == evidenceID {
						return fmt.Errorf("%w: required business evidence already used by another report", ErrInvalidArgument)
					}
				}
			}
		}
		key := id + "/" + report.ID
		if prior, exists := data.workflowStateReports[key]; exists {
			if prior.Fingerprint != fingerprint {
				return ErrOperationInputConflict
			}
		} else if err := m.validateWorkflowResolutionSourcesLocked(op, canonical); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemStore) ReportOperationWorkflowState(_ context.Context, id string, authority OperationExecutionAuthority, report api.OperationWorkflowStateReport) (api.OperationWorkflowStateReportResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, def, err := m.milestoneAuthorityLocked(id, authority)
	if err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	report, fingerprint, err := canonicalOperationWorkflowState(op, def, report)
	if err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	data := m.operationMemoryLocked()
	payloads := map[string][]byte{}
	milestoneNames := make(map[string]string, len(data.milestones[id]))
	for milestoneID, receipt := range data.milestones[id] {
		milestoneNames[milestoneID] = receipt.Milestone.Name
		payloads[milestoneID] = receipt.Milestone.Payload
	}
	if err := validateWorkflowPolicyEvidence(def, report, payloads); err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	if err := validatePublishedWorkflowEvidence(report, milestoneNames); err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	for _, evidenceID := range requiredWorkflowEvidenceIDs(def, report) {
		for _, prior := range data.workflowStateReports {
			if prior.History.OperationID != id || prior.History.ID == report.ID {
				continue
			}
			for _, used := range prior.History.EvidenceMilestones {
				if used.ID == evidenceID {
					return api.OperationWorkflowStateReportResponse{}, fmt.Errorf("%w: required business evidence already used by another report", ErrInvalidArgument)
				}
			}
		}
	}
	reportKey := id + "/" + report.ID
	if prior, exists := data.workflowStateReports[reportKey]; exists {
		if prior.Fingerprint != fingerprint {
			return api.OperationWorkflowStateReportResponse{}, ErrOperationInputConflict
		}
		response := prior.Response
		response.Blockers = append([]api.OperationWorkflowBlocker(nil), response.Blockers...)
		response.DependsOn = append([]api.OperationWorkflowDependency(nil), response.DependsOn...)
		response.BlockerResolutions = append([]api.OperationWorkflowBlockerResolution(nil), response.BlockerResolutions...)
		return response, nil
	}
	if err := m.validateWorkflowResolutionSourcesLocked(op, report); err != nil {
		return api.OperationWorkflowStateReportResponse{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	response := api.OperationWorkflowStateReportResponse{ID: report.ID, OperationID: id, Workflow: report.Workflow,
		InstanceID: report.InstanceID, FromState: report.FromState, State: report.State, Revision: report.Revision,
		Blockers: append([]api.OperationWorkflowBlocker(nil), report.Blockers...), BlockerResolutions: append([]api.OperationWorkflowBlockerResolution(nil), report.BlockerResolutions...), DependsOn: append([]api.OperationWorkflowDependency(nil), report.DependsOn...), DependenciesOnly: report.DependenciesOnly, OutcomeCode: report.OutcomeCode, OutcomeDescription: report.OutcomeDescription, OutcomeOnly: report.OutcomeOnly, DeadlineAt: report.DeadlineAt, DeadlineOnly: report.DeadlineOnly, BlockersOnly: report.BlockersOnly, ContractVersion: report.ContractVersion, EvidenceMilestones: append([]api.OperationWorkflowEvidenceMilestone(nil), report.EvidenceMilestones...)}
	history := api.OperationWorkflowStateHistoryEntry{ID: report.ID, OperationID: id, Workflow: report.Workflow,
		InstanceID: report.InstanceID, FromState: report.FromState, State: report.State, Revision: report.Revision,
		Blockers: append([]api.OperationWorkflowBlocker(nil), report.Blockers...), BlockerResolutions: append([]api.OperationWorkflowBlockerResolution(nil), report.BlockerResolutions...), DependsOn: append([]api.OperationWorkflowDependency(nil), report.DependsOn...), DependenciesOnly: report.DependenciesOnly, OutcomeCode: report.OutcomeCode, OutcomeDescription: report.OutcomeDescription, OutcomeOnly: report.OutcomeOnly, DeadlineAt: report.DeadlineAt, DeadlineOnly: report.DeadlineOnly, BlockersOnly: report.BlockersOnly, ContractVersion: report.ContractVersion, EvidenceMilestones: append([]api.OperationWorkflowEvidenceMilestone(nil), report.EvidenceMilestones...),
		OccurredAt: report.OccurredAt, PublishedAt: now}
	stateKey := operationWorkflowStateKey(op.AccountID, op.AppID, op.PlatformTenantID, op.Scope, op.Subject.Type, op.Subject.ID, report.Workflow, report.InstanceID)
	prior, exists := data.workflowStates[stateKey]
	if !exists || report.Revision > prior.State.Revision || report.Revision == prior.State.Revision && report.ID > prior.ReportID {
		data.workflowStates[stateKey] = operationWorkflowStateRecord{
			State: api.OperationWorkflowState{ReportID: report.ID, OperationID: id, Workflow: report.Workflow, InstanceID: report.InstanceID, State: report.State,
				Terminal: operations.OperationWorkflowStateIsTerminal(def.Spec, report.Workflow, report.State), OccurredAt: report.OccurredAt,
				StaleAfterSeconds: operations.OperationWorkflowStateStaleAfterSeconds(def.Spec, report.Workflow, report.State),
				Revision:          report.Revision, Blockers: append([]api.OperationWorkflowBlocker(nil), report.Blockers...), BlockerResolutions: append([]api.OperationWorkflowBlockerResolution(nil), report.BlockerResolutions...), DependsOn: append([]api.OperationWorkflowDependency(nil), report.DependsOn...), DependenciesOnly: report.DependenciesOnly, OutcomeCode: report.OutcomeCode, OutcomeDescription: report.OutcomeDescription, OutcomeOnly: report.OutcomeOnly, DeadlineAt: report.DeadlineAt, DeadlineOnly: report.DeadlineOnly, BlockersOnly: report.BlockersOnly, ContractVersion: report.ContractVersion,
				EvidenceMilestones: append([]api.OperationWorkflowEvidenceMilestone(nil), report.EvidenceMilestones...), UpdatedAt: now},
			OperationID: id, ReportID: report.ID, AccountID: op.AccountID, AppID: op.AppID, TenantID: op.PlatformTenantID,
			Scope: op.Scope, SubjectType: op.Subject.Type, SubjectID: op.Subject.ID,
		}
	}
	data.workflowStateReports[reportKey] = operationWorkflowStateReceipt{Response: response, Fingerprint: fingerprint, History: history}
	response.Blockers = append([]api.OperationWorkflowBlocker(nil), response.Blockers...)
	response.DependsOn = append([]api.OperationWorkflowDependency(nil), response.DependsOn...)
	response.BlockerResolutions = append([]api.OperationWorkflowBlockerResolution(nil), response.BlockerResolutions...)
	return response, nil
}

func (m *MemStore) ValidateOperationMilestones(_ context.Context, id string, authority OperationExecutionAuthority, reports []api.OperationMilestoneRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, def, err := m.milestoneAuthorityLocked(id, authority)
	if err != nil {
		return err
	}
	if err := validateOperationMilestoneBatch(op, def, reports); err != nil {
		return err
	}
	fresh := 0
	for _, report := range reports {
		_, fingerprint, err := canonicalOperationMilestone(op, def, report)
		if err != nil {
			return err
		}
		if prior, exists := m.operationData.milestones[id][report.ID]; exists {
			if prior.Fingerprint != fingerprint {
				return ErrOperationInputConflict
			}
		} else {
			if err := m.validateCompensationSourceLocked(op, report); err != nil {
				return err
			}
			fresh++
		}
	}
	if op.MilestoneCount+fresh > api.OperationMilestonesMaxPerOperation {
		return NewOperationLimitError("milestones_per_operation", api.OperationMilestonesMaxPerOperation, int64(op.MilestoneCount+fresh))
	}
	return nil
}

func (m *MemStore) ReportOperationMilestone(_ context.Context, id string, authority OperationExecutionAuthority, report api.OperationMilestoneRequest) (api.OperationMilestone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, inv, def, err := m.milestoneAuthorityLocked(id, authority)
	if err != nil {
		return api.OperationMilestone{}, err
	}
	report, fingerprint, err := canonicalOperationMilestone(op, def, report)
	if err != nil {
		return api.OperationMilestone{}, err
	}
	data := m.operationData
	if prior, exists := data.milestones[id][report.ID]; exists {
		if prior.Fingerprint != fingerprint {
			return api.OperationMilestone{}, ErrOperationInputConflict
		}
		return cloneOperationMilestone(prior.Milestone), nil
	}
	if err := m.validateCompensationSourceLocked(op, report); err != nil {
		return api.OperationMilestone{}, err
	}
	milestone, event, err := newOperationMilestone(&op, inv, def, report, time.Now().UTC())
	if err != nil {
		return milestone, err
	}
	if data.milestones[id] == nil {
		data.milestones[id] = map[string]operationMilestoneReceipt{}
	}
	data.milestones[id][report.ID] = operationMilestoneReceipt{Milestone: cloneOperationMilestone(milestone), Fingerprint: fingerprint}
	m.operationSaveLocked(op, event)
	return cloneOperationMilestone(milestone), nil
}

func (m *MemStore) ListPlatformTenantOperationMilestones(_ context.Context, account, tenant string, opts api.OperationMilestoneListOptions) (api.OperationMilestonesResponse, error) {
	return m.listOperationMilestones(account, tenant, opts, false)
}
func (m *MemStore) ListAccountOperationMilestones(_ context.Context, account string, opts api.OperationMilestoneListOptions) (api.OperationMilestonesResponse, error) {
	return m.listOperationMilestones(account, opts.TenantID, opts, true)
}

func (m *MemStore) listOperationMilestones(account, tenant string, opts api.OperationMilestoneListOptions, operator bool) (api.OperationMilestonesResponse, error) {
	opts, cursor, err := prepareOperationMilestoneHistory(account, tenant, opts, operator)
	if err != nil {
		return api.OperationMilestonesResponse{}, err
	}
	stateHistoryCursor, err := prepareOperationWorkflowStateHistoryCursor(account, tenant, opts, operator)
	if err != nil {
		return api.OperationMilestonesResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []api.OperationMilestone{}
	workflowStates := []api.OperationWorkflowState{}
	workflowStateHistory := []api.OperationWorkflowStateHistoryEntry{}
	var workflowInstanceState *api.OperationWorkflowState
	retainedWorkflowFacts := make(map[int]map[string]*operationWorkflowStepFactSummary)
	now := time.Now().UTC()
	data := m.operationMemoryLocked()
	for id, receipts := range data.milestones {
		op := data.operations[id]
		if !sameOperationHistoryIdentity(op.AccountID, account) || tenant != "" && !sameOperationHistoryIdentity(op.PlatformTenantID, tenant) || !sameOperationHistoryIdentity(op.AppID, opts.AppID) || op.Scope != opts.Scope || !operationRetained(op, now) || opts.OperationID != "" && opts.OperationID != op.ID {
			continue
		}
		if opts.SubjectType != "" && (op.Subject == nil || op.Subject.Type != opts.SubjectType || op.Subject.ID != opts.SubjectID) {
			continue
		}
		for _, receipt := range receipts {
			row := cloneOperationMilestone(receipt.Milestone)
			if opts.Workflow != "" && !operationMilestoneMatchesWorkflow(row, opts.Workflow, opts.WorkflowInstanceID) {
				continue
			}
			if opts.Workflow != "" {
				row.WorkflowSteps = filterOperationWorkflowSteps(row.WorkflowSteps, opts.Workflow, opts.WorkflowInstanceID)
				addOperationWorkflowStepFactSummary(retainedWorkflowFacts, row, opts.Workflow, opts.WorkflowInstanceID)
			}
			if cursor.ID != "" && (!row.CreatedAt.Before(cursor.CreatedAt) && (!row.CreatedAt.Equal(cursor.CreatedAt) || row.OperationID > cursor.OperationID || row.OperationID == cursor.OperationID && row.ID >= cursor.ID)) {
				continue
			}
			if operator {
				row.PlatformTenantID = op.PlatformTenantID
			}
			rows = append(rows, row)
		}
	}
	if opts.SubjectType != "" {
		for _, record := range data.workflowStates {
			if !sameOperationHistoryIdentity(record.AccountID, account) || tenant != "" && !sameOperationHistoryIdentity(record.TenantID, tenant) ||
				!sameOperationHistoryIdentity(record.AppID, opts.AppID) || record.Scope != opts.Scope || record.SubjectType != opts.SubjectType || record.SubjectID != opts.SubjectID ||
				opts.Workflow != "" && (record.State.Workflow != opts.Workflow || record.State.InstanceID != opts.WorkflowInstanceID) {
				continue
			}
			state := record.State
			state.Blockers = append([]api.OperationWorkflowBlocker(nil), state.Blockers...)
			state.DependsOn = append([]api.OperationWorkflowDependency(nil), state.DependsOn...)
			state.BlockerResolutions = append([]api.OperationWorkflowBlockerResolution(nil), state.BlockerResolutions...)
			state.Stale = operationWorkflowStateIsStale(now, state)
			evaluateOperationWorkflowDeadline(now, &state)
			if operator {
				state.PlatformTenantID = record.TenantID
			}
			if opts.Workflow != "" && state.Workflow == opts.Workflow && state.InstanceID == opts.WorkflowInstanceID {
				copy := state
				workflowInstanceState = &copy
			}
			if opts.WorkflowStaleOnly && !state.Stale {
				continue
			}
			workflowStates = append(workflowStates, state)
		}
		sort.Slice(workflowStates, func(i, j int) bool {
			if workflowStates[i].UpdatedAt.Equal(workflowStates[j].UpdatedAt) {
				if workflowStates[i].Workflow == workflowStates[j].Workflow {
					return workflowStates[i].InstanceID < workflowStates[j].InstanceID
				}
				return workflowStates[i].Workflow < workflowStates[j].Workflow
			}
			return workflowStates[i].UpdatedAt.After(workflowStates[j].UpdatedAt)
		})
		if len(workflowStates) > opts.Limit {
			workflowStates = workflowStates[:opts.Limit]
		}
		if opts.Workflow != "" {
			for _, receipt := range data.workflowStateReports {
				entry := receipt.History
				entry.Blockers = append([]api.OperationWorkflowBlocker(nil), entry.Blockers...)
				entry.DependsOn = append([]api.OperationWorkflowDependency(nil), entry.DependsOn...)
				entry.BlockerResolutions = append([]api.OperationWorkflowBlockerResolution(nil), entry.BlockerResolutions...)
				op, exists := data.operations[entry.OperationID]
				if !exists || !sameOperationHistoryIdentity(op.AccountID, account) || tenant != "" && !sameOperationHistoryIdentity(op.PlatformTenantID, tenant) ||
					!sameOperationHistoryIdentity(op.AppID, opts.AppID) || op.Scope != opts.Scope || !operationRetained(op, now) || op.Subject == nil ||
					op.Subject.Type != opts.SubjectType || op.Subject.ID != opts.SubjectID || entry.Workflow != opts.Workflow || entry.InstanceID != opts.WorkflowInstanceID ||
					!workflowStateHistoryAfterCursor(entry, stateHistoryCursor) {
					continue
				}
				if operator {
					entry.PlatformTenantID = op.PlatformTenantID
				}
				workflowStateHistory = append(workflowStateHistory, entry)
			}
			sort.Slice(workflowStateHistory, func(i, j int) bool {
				a, b := workflowStateHistory[i], workflowStateHistory[j]
				if a.Revision != b.Revision {
					return a.Revision < b.Revision
				}
				if !a.PublishedAt.Equal(b.PublishedAt) {
					return a.PublishedAt.Before(b.PublishedAt)
				}
				if a.OperationID != b.OperationID {
					return a.OperationID < b.OperationID
				}
				return a.ID < b.ID
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			if rows[i].OperationID == rows[j].OperationID {
				return rows[i].ID > rows[j].ID
			}
			return rows[i].OperationID > rows[j].OperationID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	page := operationMilestonePage(rows, opts.Limit, cursor)
	page.WorkflowStates = workflowStates
	if opts.Workflow != "" {
		page.WorkflowStateHistory, page.NextWorkflowStateCursor = operationWorkflowStateHistoryPage(workflowStateHistory, opts.Limit, stateHistoryCursor)
		declarations := make([]operationWorkflowStepDeclaration, 0)
		for _, definition := range data.definitions {
			deployment, exists := m.deployments[definition.DeploymentID]
			if !sameOperationHistoryIdentity(definition.AccountID, account) || !sameOperationHistoryIdentity(definition.AppID, opts.AppID) || definition.Scope != opts.Scope {
				continue
			}
			for _, step := range definition.Spec.WorkflowSteps {
				if step.Workflow == opts.Workflow {
					declarations = append(declarations, operationWorkflowStepDeclaration{Operation: definition.Spec.Name, Spec: step,
						Active: exists && deployment.Status == DeployLive && deployment.TrafficPercent > 0})
				}
			}
		}
		sort.Slice(declarations, func(i, j int) bool {
			if declarations[i].Active != declarations[j].Active {
				return declarations[i].Active
			}
			if declarations[i].Spec.Version != declarations[j].Spec.Version {
				return declarations[i].Spec.Version > declarations[j].Spec.Version
			}
			if declarations[i].Spec.Step != declarations[j].Spec.Step {
				return declarations[i].Spec.Step < declarations[j].Spec.Step
			}
			return declarations[i].Operation < declarations[j].Operation
		})
		page = projectOperationWorkflowInstance(page, opts.Workflow, opts.WorkflowInstanceID, declarations, workflowInstanceState,
			operationWorkflowStepFactSummaryList(retainedWorkflowFacts))
	}
	m.projectRelatedWorkflowsLocked(&page, account, tenant, opts, operator, now)
	if !opts.ReadinessOnly {
		m.projectDependencyImpactLocked(&page, account, tenant, opts, operator, now)
		m.projectDependencyTraceLocked(&page, account, tenant, opts, operator, now)
		projectOperationWorkflowReadiness(&page)
	}
	return page, nil
}

func (m *MemStore) validateWorkflowResolutionSourcesLocked(op Operation, report api.OperationWorkflowStateReport) error {
	data := m.operationMemoryLocked()
	for _, resolution := range report.BlockerResolutions {
		source, ok := data.workflowStateReports[resolution.BlockerOperationID+"/"+resolution.BlockerReportID]
		sourceOp, opExists := data.operations[resolution.BlockerOperationID]
		if !ok || !opExists || !sameOperationHistoryIdentity(op.AccountID, sourceOp.AccountID) || !sameOperationHistoryIdentity(op.AppID, sourceOp.AppID) || !sameOperationHistoryIdentity(op.PlatformTenantID, sourceOp.PlatformTenantID) || op.Scope != sourceOp.Scope || op.Subject == nil || sourceOp.Subject == nil || *op.Subject != *sourceOp.Subject || !operationRetained(sourceOp, time.Now().UTC()) || source.History.Workflow != report.Workflow || source.History.InstanceID != report.InstanceID || source.History.ContractVersion != report.ContractVersion || source.History.Revision != resolution.BlockerRevision {
			return fmt.Errorf("%w: workflow resolution source report is not retained in this workflow and owner boundary", ErrInvalidArgument)
		}
		if !workflowResolutionBlockerExists(source.History.Blockers, resolution) {
			return fmt.Errorf("%w: referenced report did not contain this blocker", ErrInvalidArgument)
		}
	}
	return nil
}
