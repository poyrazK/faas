// adr: 1302
package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func baselineKey(appID, name, alias string) string { return appID + "/" + name + "/" + alias }

func (m *MemStore) GetAutomationLiveBaseline(ctx context.Context, appID, name, alias string) (api.AutomationLiveBaseline, error) {
	history, err := m.GetAutomationLiveBaselineHistory(ctx, appID, name, alias)
	if err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	if history.Retirement != nil {
		return api.AutomationLiveBaseline{}, baselineRetiredError()
	}
	return history.Promotions[0], nil
}
func (m *MemStore) GetAutomationLiveBaselineHistory(ctx context.Context, appID, name, alias string) (api.AutomationLiveBaselineHistory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := api.AutomationLiveBaselineHistory{Promotions: []api.AutomationLiveBaseline{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !api.ValidAutomationLiveBaselineName(alias) {
		return out, ErrNotFound
	}
	if _, ok := m.automations[appID+"/"+name]; !ok {
		return out, ErrNotFound
	}
	records := m.automationLiveBaselines[baselineKey(appID, name, alias)]
	if len(records) == 0 {
		return out, ErrNotFound
	}
	if receipt, ok := m.automationLiveBaselineRetirements[baselineKey(appID, name, alias)]; ok {
		out.Retirement = &receipt
	}
	for i := len(records) - 1; i >= 0; i-- {
		out.Promotions = append(out.Promotions, cloneBaseline(records[i]))
	}
	return out, nil
}
func (m *MemStore) PromoteAutomationLiveBaseline(ctx context.Context, appID, name, alias, promotionID, actor, key string, request api.PromoteAutomationLiveBaselineRequest) (api.AutomationLiveBaseline, error) {
	policy, err := validateBaselinePromotion(alias, promotionID, actor, key, &request)
	if err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	promotionID = uuid.MustParse(promotionID).String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	if _, ok := m.automations[appID+"/"+name]; !ok {
		return api.AutomationLiveBaseline{}, ErrNotFound
	}
	count, total := 0, 0
	for _, records := range m.automationLiveBaselines {
		for _, receipt := range records {
			if receipt.PromotionID == promotionID {
				if receipt.AppID != appID || receipt.Name != name {
					return api.AutomationLiveBaseline{}, ErrNotFound
				}
				if !baselineReplay(receipt, appID, name, alias, actor, key, request, policy) {
					return api.AutomationLiveBaseline{}, ErrAutomationLiveBaselineConflict
				}
				return cloneBaseline(receipt), nil
			}
		}
		if len(records) > 0 && records[0].AppID == appID && records[0].Name == name {
			total++
			if _, retired := m.automationLiveBaselineRetirements[baselineKey(appID, name, records[0].BaselineName)]; !retired {
				count++
			}
		}
	}
	mapKey := baselineKey(appID, name, alias)
	if _, retired := m.automationLiveBaselineRetirements[mapKey]; retired {
		return api.AutomationLiveBaseline{}, baselineRetiredError()
	}
	records := m.automationLiveBaselines[mapKey]
	if len(records) == 0 && total >= api.AutomationLiveBaselineLifetimeNameLimit {
		return api.AutomationLiveBaseline{}, baselineLifetimeLimitError(int64(total + 1))
	}
	var prior api.AutomationLiveBaseline
	if len(records) > 0 {
		prior = records[len(records)-1]
	} else if count >= api.AutomationLiveBaselineLimit {
		return api.AutomationLiveBaseline{}, baselineLimitError(int64(count + 1))
	}
	if prior.Revision != request.ExpectedRevision {
		return api.AutomationLiveBaseline{}, ErrAutomationLiveBaselineConflict
	}
	suite, ok := m.automationLiveSuites[request.SuiteID]
	if !ok || suite.AppID != appID || suite.Name != name {
		return api.AutomationLiveBaseline{}, ErrNotFound
	}
	view, err := m.liveSuiteViewLocked(suite)
	if err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	receipt, err := promotedBaseline(appID, name, alias, promotionID, actor, key, request, policy, prior, view)
	if err != nil {
		return receipt, err
	}
	if m.automationLiveBaselines == nil {
		m.automationLiveBaselines = map[string][]api.AutomationLiveBaseline{}
	}
	records = append(records, receipt)
	if len(records) > api.AutomationLiveBaselineHistoryLimit {
		records = records[len(records)-api.AutomationLiveBaselineHistoryLimit:]
	}
	m.automationLiveBaselines[mapKey] = records
	return cloneBaseline(receipt), nil
}
func (m *MemStore) liveSuitePinnedLocked(id string) bool {
	for key, records := range m.automationLiveBaselines {
		if _, retired := m.automationLiveBaselineRetirements[key]; retired {
			continue
		}
		if len(records) > 0 && records[len(records)-1].Suite.ID == id {
			return true
		}
	}
	return false
}

func (m *MemStore) clearAutomationLiveBaselinesLocked(appID, name string) {
	for key, records := range m.automationLiveBaselines {
		if len(records) > 0 && records[0].AppID == appID && records[0].Name == name {
			delete(m.automationLiveBaselines, key)
			delete(m.automationLiveBaselineRetirements, key)
		}
	}
}
