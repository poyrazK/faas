package state

import (
	"context"
	"encoding/json"
)

func (m *MemStore) completeInvocationContext(ctx context.Context, id string, attempt int, claim *InvocationClaim, result json.RawMessage, classification ...InvocationWorkClassification) error {
	return m.completeInvocationAndQueueServingAck(ctx, id, attempt, claim, result, nil, classification...)
}

func (m *MemStore) queueOwnershipErasureBlockedLocked(accountID string) bool {
	for _, binding := range m.queueBindings {
		app, known := m.apps[binding.AppID]
		if binding.AccountID == accountID && (!known || !m.personalAppLocked(app) && !m.orgs[app.OrgID].ResourceOwnershipEnabled) {
			return true
		}
	}
	for _, trigger := range m.triggers {
		if !trigger.AccountID.Valid || trigger.AccountID.String() != canonicalMemUUID(accountID) {
			continue
		}
		app, known := m.triggerAppLocked(trigger)
		if known && m.personalAppLocked(app) {
			continue
		}
		var binding QueueBinding
		bound := false
		for _, candidate := range m.queueBindings {
			if canonicalMemUUID(candidate.ID) == trigger.QueueBindingID.String() {
				binding, bound = candidate, true
				break
			}
		}
		if !known || !bound || trigger.Kind != "queue" || trigger.Source.String != "queue" || binding.AppID != app.ID || binding.AccountID != app.AccountID {
			return true
		}
	}
	return false
}

func (m *MemStore) purgePersonalQueuesLocked(accountID string) {
	personal := func(appID string) bool {
		app, ok := m.apps[appID]
		return ok && app.AccountID == accountID && m.personalAppLocked(app)
	}
	for id, binding := range m.queueBindings {
		if personal(binding.AppID) {
			delete(m.queueBindings, id)
		}
	}
	for id, trigger := range m.triggers {
		app, known := m.triggerAppLocked(trigger)
		if !known || !personal(app.ID) {
			continue
		}
		for recordID, record := range m.records {
			if record.TriggerID == trigger.ID {
				delete(m.records, recordID)
			}
		}
		kept := m.triggerDeadLetters[:0]
		for _, row := range m.triggerDeadLetters {
			if row.TriggerID != trigger.ID {
				kept = append(kept, row)
			}
		}
		m.triggerDeadLetters = kept
		delete(m.triggers, id)
	}
	for id, set := range m.projectEnvironmentQueueRuntimeSets {
		if personal(set.AppID) {
			delete(m.projectEnvironmentQueueRuntimeSets, id)
		}
	}
	for id, inv := range m.invocations {
		if personal(inv.AppID) {
			if inv.QuotaReserved {
				m.decrementAccountAsyncInflightLocked(invocationAsyncSubject(inv))
			}
			delete(m.invocations, id)
			delete(m.eventDeliverySlots, id)
			m.deleteInvocationAttemptsLocked(id)
			delete(m.keyedReplayChildren, id)
			delete(m.plainReplayChildren, id)
			delete(m.invocationWorkEnvironmentAdmissions, id)
			delete(m.invocationEnvironmentQueueAdmissions, id)
			delete(m.invocationEnvironmentQueueReceipts, id)
			delete(m.invocationQueueLeaseReceipts, id)
		}
	}
}
