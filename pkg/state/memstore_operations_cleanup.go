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
	delete(data.milestones, id)
	for key := range data.workflowStateReports {
		if strings.HasPrefix(key, id+"/") {
			delete(data.workflowStateReports, key)
		}
	}
	delete(data.events, id)
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
	for key, record := range data.workflowStates {
		if owned(record.AccountID, record.AppID) {
			delete(data.workflowStates, key)
		}
	}
}
