// adr: 1305
package state

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) RetireAutomationLiveBaseline(ctx context.Context, appID, name, alias, id, actor, key string, request api.RetireAutomationLiveBaselineRequest) (api.AutomationLiveBaselineRetirement, error) {
	var out api.AutomationLiveBaselineRetirement
	if err := validateBaselineRetirement(alias, retirementIdentityValues(id, actor, key), request.ExpectedRevision); err != nil {
		return out, err
	}
	id = uuid.MustParse(id).String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if _, ok := m.automations[appID+"/"+name]; !ok {
		return out, ErrNotFound
	}
	for _, receipt := range m.automationLiveBaselineRetirements {
		if receipt.RetirementID != id {
			continue
		}
		if receipt.AppID != appID || receipt.Name != name {
			return out, ErrNotFound
		}
		if !retirementReplay(receipt, appID, name, alias, id, actor, key, request.ExpectedRevision) {
			return out, ErrAutomationLiveBaselineConflict
		}
		return receipt, nil
	}
	mapKey := baselineKey(appID, name, alias)
	if _, ok := m.automationLiveBaselineRetirements[mapKey]; ok {
		return out, baselineRetiredError()
	}
	records := m.automationLiveBaselines[mapKey]
	if len(records) == 0 {
		return out, ErrNotFound
	}
	prior := records[len(records)-1]
	if prior.Revision != request.ExpectedRevision {
		return out, ErrAutomationLiveBaselineConflict
	}
	out = retiredBaseline(prior, id, actor, key)
	if m.automationLiveBaselineRetirements == nil {
		m.automationLiveBaselineRetirements = map[string]api.AutomationLiveBaselineRetirement{}
	}
	m.automationLiveBaselineRetirements[mapKey] = out
	return out, nil
}
