// adr: 1305
package state

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) RetireStoredWorkflowLiveBaseline(ctx context.Context, appID, name, alias, id, actor, key string, request api.RetireStoredWorkflowLiveBaselineRequest) (api.StoredWorkflowLiveBaselineRetirement, error) {
	var out api.StoredWorkflowLiveBaselineRetirement
	if err := validateBaselineRetirement(alias, retirementIdentityValues(id, actor, key), request.ExpectedRevision); err != nil {
		return out, err
	}
	id = uuid.MustParse(id).String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if _, ok := m.storedWorkflows[appID+"/"+name]; !ok {
		return out, ErrNotFound
	}
	for _, receipt := range m.storedWorkflowLiveBaselineRetirements {
		if receipt.RetirementID != id {
			continue
		}
		if receipt.AppID != appID || receipt.Name != name {
			return out, ErrNotFound
		}
		if !retirementReplay(receipt, appID, name, alias, id, actor, key, request.ExpectedRevision) {
			return out, ErrStoredWorkflowLiveBaselineConflict
		}
		return receipt, nil
	}
	mapKey := baselineKey(appID, name, alias)
	if _, ok := m.storedWorkflowLiveBaselineRetirements[mapKey]; ok {
		return out, baselineRetiredError()
	}
	records := m.storedWorkflowLiveBaselines[mapKey]
	if len(records) == 0 {
		return out, ErrNotFound
	}
	prior := records[len(records)-1]
	if prior.Revision != request.ExpectedRevision {
		return out, ErrStoredWorkflowLiveBaselineConflict
	}
	out = retiredBaseline(prior, id, actor, key)
	if m.storedWorkflowLiveBaselineRetirements == nil {
		m.storedWorkflowLiveBaselineRetirements = map[string]api.StoredWorkflowLiveBaselineRetirement{}
	}
	m.storedWorkflowLiveBaselineRetirements[mapKey] = out
	return out, nil
}
