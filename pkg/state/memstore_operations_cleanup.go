package state

import "strings"

func (m *MemStore) forgetOperationLocked(id string) {
	data := m.operationData
	if data == nil {
		return
	}
	for key, receipt := range data.deliveryRetries {
		if receipt.OperationID == id {
			delete(data.deliveryRetries, key)
		}
	}
	delete(data.operations, id)
	delete(data.events, id)
	delete(data.workflowExecutions, id)
	delete(data.jobExecutions, id)
	for runID, owner := range data.jobOwners {
		if owner == id {
			delete(data.jobOwners, runID)
		}
	}
	for key, lease := range data.streams {
		if lease.OperationID == id {
			delete(data.streams, key)
		}
	}
	for inv, op := range data.executions {
		if op == id {
			delete(data.executions, inv)
			delete(data.generations, inv)
		}
	}
	for key := range data.reports {
		if strings.HasPrefix(key, id+"/") {
			delete(data.reports, key)
		}
	}
	for key := range data.recoveries {
		if strings.HasPrefix(key, id+"/") {
			delete(data.recoveries, key)
			delete(data.recoveryDecisions, key)
		}
	}
}

func (m *MemStore) forgetOwnedOperationsLocked(account, app string) {
	data := m.operationData
	if data == nil {
		return
	}
	owned := func(acct, appID string) bool { return account != "" && acct == account || app != "" && appID == app }
	for id, op := range data.operations {
		if owned(op.AccountID, op.AppID) {
			if op.WorkflowRunID != "" {
				m.forgetOwnedOperationWorkflowLocked(op.WorkflowRunID)
			}
			m.forgetOperationLocked(id)
		}
	}
	for id, def := range data.definitions {
		if owned(def.AccountID, def.AppID) {
			delete(data.definitions, id)
		}
	}
	for key, receipt := range data.receipts {
		if owned(receipt.AccountID, receipt.AppID) {
			delete(data.receipts, key)
		}
	}
}

// Permanent owner deletion mirrors the native workflow FK cascades. Ordinary
// result expiry only releases the association for the workflow retention sweep.
func (m *MemStore) forgetOwnedOperationWorkflowLocked(runID string) {
	delete(m.workflowRuns, runID)
	delete(m.workflowSteps, runID)
	delete(m.workflowEvents, runID)
	delete(m.workflowResumes, runID)
	delete(m.workflowRunLeases, runID)
	for key := range m.workflowStepAttempts {
		if key.runID == runID {
			delete(m.workflowStepAttempts, key)
			delete(m.workflowOperationEffects, key)
		}
	}
	for id, binding := range m.workflowCallbackWebhookBindings {
		if binding.RunID == runID {
			delete(m.workflowCallbackWebhookBindings, id)
		}
	}
}
