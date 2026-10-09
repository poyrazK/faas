package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

type operationMemory struct {
	deliveryRetries      map[string]api.OperationDeliveryRetryResponse
	definitions          map[string]OperationDefinition
	operations           map[string]Operation
	events               map[string][]api.OperationEvent
	receipts             map[string]operationIdentityReceipt
	executions           map[string]string
	generations          map[string]int
	reports              map[string]string
	recoveries           map[string]string
	recoveryDecisions    map[string]api.OperationRecoveryDecision
	streams              map[string]operationStreamLease
	jobOwners            map[string]string
	jobExecutions        map[string]map[int]api.OperationExecution
	workflowExecutions   map[string]map[int]api.OperationExecution
	blobs                map[string]OperationResultBlob
	milestones           map[string]map[string]operationMilestoneReceipt
	workflowStates       map[string]operationWorkflowStateRecord
	workflowStateReports map[string]operationWorkflowStateReceipt
}

func (m *MemStore) operationMemoryLocked() *operationMemory {
	if m.operationData == nil {
		m.operationData = &operationMemory{milestones: map[string]map[string]operationMilestoneReceipt{}, workflowStates: map[string]operationWorkflowStateRecord{}, workflowStateReports: map[string]operationWorkflowStateReceipt{}, definitions: map[string]OperationDefinition{}, operations: map[string]Operation{}, events: map[string][]api.OperationEvent{}, receipts: map[string]operationIdentityReceipt{}, executions: map[string]string{}, generations: map[string]int{}, reports: map[string]string{}, recoveries: map[string]string{}, blobs: map[string]OperationResultBlob{}}
	}
	return m.operationData
}

func (m *MemStore) PutOperationDefinition(ctx context.Context, def OperationDefinition) (OperationDefinition, error) {
	if def.ReleaseID != "" {
		_, deployment, err := m.ResolveProjectRelease(ctx, def.AppID, def.Scope, def.ReleaseID)
		if err != nil || deployment != def.DeploymentID {
			return OperationDefinition{}, ErrConflict
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[def.AppID]
	if !ok || app.AccountID != def.AccountID || app.Status == AppDeleted {
		return OperationDefinition{}, ErrNotFound
	}
	dep, ok := m.deployments[def.DeploymentID]
	if !ok || dep.AppID != def.AppID || dep.Scope != def.Scope {
		return OperationDefinition{}, ErrNotFound
	}
	acct := m.accounts[def.AccountID]
	limits := api.MustLimitsFor(acct.Plan)
	contract, err := operations.Compile(def.Spec, limits.Operations)
	if err != nil {
		return OperationDefinition{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if id := contract.Spec.CompletionWebhookID; id != "" {
		hook, exists := m.appWebhooks[id]
		if !exists || hook.AppID != def.AppID || hook.AccountID != def.AccountID || !hook.Enabled {
			return OperationDefinition{}, ErrNotFound
		}
	}
	def.Spec, def.Revision = contract.Spec, contract.Revision
	if def.Spec.Workflow != "" {
		if _, err := operationWorkflowDefinition(def, dep.Workflows, acct.Plan); err != nil {
			return OperationDefinition{}, err
		}
	}
	data := m.operationMemoryLocked()
	count := 0
	names := map[string]bool{}
	for _, existing := range data.definitions {
		if existing.AppID != def.AppID || existing.Scope != def.Scope {
			continue
		}
		names[existing.Spec.Name] = true
		if existing.Spec.Name == def.Spec.Name && existing.DeploymentID == def.DeploymentID {
			if existing.Revision != def.Revision || existing.ReleaseID != def.ReleaseID {
				return OperationDefinition{}, ErrConflict
			}
			return cloneOperationDefinition(existing), nil
		}
	}
	count = len(names)
	if !names[def.Spec.Name] && count >= limits.Operations.DefinitionsPerApp {
		return OperationDefinition{}, NewOperationLimitError("definitions_per_app", int64(limits.Operations.DefinitionsPerApp), int64(count)+1)
	}
	for _, existing := range data.definitions {
		if existing.DeploymentID == def.DeploymentID && existing.Spec.Method == def.Spec.Method && existing.Spec.Path == def.Spec.Path {
			return OperationDefinition{}, ErrConflict
		}
	}
	if def.ID == "" {
		def.ID = newOperationID()
	}
	if _, exists := data.definitions[def.ID]; exists {
		return OperationDefinition{}, ErrConflict
	}
	def.CreatedAt = time.Now().UTC()
	data.definitions[def.ID] = cloneOperationDefinition(def)
	return cloneOperationDefinition(def), nil
}

func (m *MemStore) OperationDefinitionByID(_ context.Context, accountID, id string) (OperationDefinition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	def, ok := m.operationMemoryLocked().definitions[id]
	if !ok || def.AccountID != accountID {
		return OperationDefinition{}, ErrNotFound
	}
	return cloneOperationDefinition(def), nil
}

func (m *MemStore) OperationDefinitionForDeployment(_ context.Context, accountID, appID, deploymentID, name string) (OperationDefinition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, def := range m.operationMemoryLocked().definitions {
		if def.AccountID == accountID && def.AppID == appID && def.DeploymentID == deploymentID && def.Spec.Name == name {
			return cloneOperationDefinition(def), nil
		}
	}
	return OperationDefinition{}, ErrNotFound
}

func (m *MemStore) AdmitOperation(ctx context.Context, admission OperationAdmission) (Operation, bool, error) {

	var releaseErr error
	if admission.ReleaseID != "" {
		hint, err := m.OperationDefinitionByID(ctx, admission.AccountID, admission.DefinitionID)
		if err != nil {
			return Operation{}, false, err
		}
		_, deployment, err := m.ResolveProjectRelease(ctx, hint.AppID, hint.Scope, admission.ReleaseID)
		releaseErr = err
		if err == nil && (deployment != hint.DeploymentID || (hint.ReleaseID != "" && admission.ReleaseID != hint.ReleaseID)) {
			releaseErr = ErrConflict
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	def, ok := data.definitions[admission.DefinitionID]
	if !ok || def.AccountID != admission.AccountID {
		return Operation{}, false, ErrNotFound
	}
	tenant, ok := m.platformTenants[admission.PlatformTenantID]
	if !ok || tenant.AccountID != admission.AccountID {
		return Operation{}, false, ErrNotFound
	}
	app, exists := m.apps[def.AppID]
	if !exists || app.AccountID != admission.AccountID || app.Status == AppDeleted {
		return Operation{}, false, ErrNotFound
	}
	acct := m.accounts[admission.AccountID]
	limits := api.MustLimitsFor(acct.Plan)
	now := time.Now().UTC()
	op, inv, key, fingerprint, err := prepareOperationAdmission(def, admission, limits, now)
	if err != nil {
		return Operation{}, false, err
	}
	receipt, receiptExists := data.receipts[key]
	original, originalExists := data.operations[receipt.OperationID]
	if receiptExists && (receipt.ExpiresAt.After(now) || (originalExists && operationIsActive(original))) {
		if receipt.Fingerprint != fingerprint {
			return Operation{}, false, ErrOperationInputConflict
		}
		if !originalExists || !operationRetained(original, now) {
			return Operation{}, false, ErrOperationExpired
		}
		return cloneOperation(original), false, nil
	}
	if !limits.Operations.Allowed {
		return Operation{}, false, NewOperationLimitError("plan_admission", 0, 1)
	}
	if releaseErr != nil {
		return Operation{}, false, releaseErr
	}
	if err := m.platformTenantInvocationAllowedLocked(inv); err != nil {
		return Operation{}, false, err
	}
	if !m.operationCodeAvailableLocked(op) || (def.ReleaseID != "" && op.ReleaseID != def.ReleaseID) {
		return Operation{}, false, ErrConflict
	}
	if op.ReleaseID != "" && !releasePubliclyUsable(m.projectReleaseSets[op.ReleaseID], now) {
		return Operation{}, false, ErrNotFound
	}
	if err := validateNewOperationInput(def, admission.Input, limits); err != nil {
		return Operation{}, false, err
	}
	op.Subject, err = operations.ExtractOperationSubject(def.Spec.Subject, inv.Payload)
	if err != nil {
		return Operation{}, false, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	pending := 0
	for _, existing := range data.operations {
		if existing.AccountID == admission.AccountID && !existing.State.Terminal() {
			pending++
		}
	}
	if pending >= limits.Operations.PendingPerAccount {
		return Operation{}, false, NewOperationLimitError("pending_per_account", int64(limits.Operations.PendingPerAccount), int64(pending)+1)
	}
	var jobRun JobRun
	if def.Spec.Job != "" {
		jobRun, err = m.prepareOperationJobLocked(&op, inv, def, acct.Plan)
		if err != nil {
			return Operation{}, false, err
		}
	}
	var run WorkflowRun
	var steps []*WorkflowStep
	if def.Spec.Workflow != "" {
		run, steps, err = m.prepareOperationWorkflowLocked(&op, inv, def, acct.Plan)
		if err != nil {
			return Operation{}, false, err
		}
	}
	m.operationPinsLocked(op)
	data.operations[op.ID] = cloneOperation(op)
	data.events[op.ID] = []api.OperationEvent{initialOperationEvent(op)}
	data.receipts[key] = operationIdentityReceipt{AccountID: op.AccountID, AppID: op.AppID, OperationID: op.ID, Fingerprint: fingerprint, ExpiresAt: now.Add(time.Duration(limits.Operations.IdempotencyRetentionSeconds) * time.Second)}
	if op.JobRunID != "" {
		m.insertOperationJobLocked(op, jobRun)
	} else if op.WorkflowRunID != "" {
		m.insertOperationWorkflowLocked(op, run, steps)
	} else {
		m.invocations[inv.ID] = inv
		data.executions[inv.ID] = op.ID
		data.generations[inv.ID] = op.Generation
	}
	return cloneOperation(op), true, nil
}

func (m *MemStore) OperationByID(_ context.Context, accountID, tenantID, id string) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.operationMemoryLocked().operations[id]
	if !ok || op.AccountID != accountID || (tenantID != "" && op.PlatformTenantID != tenantID) {
		return Operation{}, ErrNotFound
	}
	if !operationRetained(op, time.Now().UTC()) {
		return Operation{}, ErrOperationExpired
	}
	return cloneOperation(m.operationDeliveryLocked(op)), nil
}

func (m *MemStore) OperationEvents(_ context.Context, accountID, tenantID, id string, after int64, limit int) (api.OperationEventsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if after < 0 || limit < 1 || limit > api.OperationEventsPageMax {
		return api.OperationEventsResponse{}, ErrInvalidArgument
	}
	data := m.operationMemoryLocked()
	op, ok := data.operations[id]
	if !ok || op.AccountID != accountID || (tenantID != "" && op.PlatformTenantID != tenantID) {
		return api.OperationEventsResponse{}, ErrNotFound
	}
	now := time.Now().UTC()
	if !operationRetained(op, now) {
		return api.OperationEventsResponse{}, ErrOperationExpired
	}
	page := api.OperationEventsResponse{Events: []api.OperationEvent{}, LatestSequence: op.LatestSequence}
	if after > op.LatestSequence || !op.EventExpiresAt.After(now) {
		page.ResyncRequired = true
		return page, nil
	}
	for _, event := range data.events[id] {
		if event.Sequence > after {
			event.Data = append([]byte(nil), event.Data...)
			page.Events = append(page.Events, event)
			if len(page.Events) >= limit {
				break
			}
		}
	}
	return page, nil
}

var _ OperationStore = (*MemStore)(nil)
