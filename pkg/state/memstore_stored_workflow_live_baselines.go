// adr: 1302
package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func baselineKey(appID, name, alias string) string { return appID + "/" + name + "/" + alias }

func (m *MemStore) GetStoredWorkflowLiveBaseline(ctx context.Context, appID, name, alias string) (api.StoredWorkflowLiveBaseline, error) {
	history, err := m.GetStoredWorkflowLiveBaselineHistory(ctx, appID, name, alias)
	if err != nil {
		return api.StoredWorkflowLiveBaseline{}, err
	}
	if history.Retirement != nil {
		return api.StoredWorkflowLiveBaseline{}, baselineRetiredError()
	}
	return history.Promotions[0], nil
}
func (m *MemStore) GetStoredWorkflowLiveBaselineHistory(ctx context.Context, appID, name, alias string) (api.StoredWorkflowLiveBaselineHistory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := api.StoredWorkflowLiveBaselineHistory{Promotions: []api.StoredWorkflowLiveBaseline{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !api.ValidStoredWorkflowLiveBaselineName(alias) {
		return out, ErrNotFound
	}
	if _, ok := m.storedWorkflows[appID+"/"+name]; !ok {
		return out, ErrNotFound
	}
	records := m.storedWorkflowLiveBaselines[baselineKey(appID, name, alias)]
	if len(records) == 0 {
		return out, ErrNotFound
	}
	if receipt, ok := m.storedWorkflowLiveBaselineRetirements[baselineKey(appID, name, alias)]; ok {
		out.Retirement = &receipt
	}
	for i := len(records) - 1; i >= 0; i-- {
		out.Promotions = append(out.Promotions, cloneBaseline(records[i]))
	}
	return out, nil
}
func (m *MemStore) PromoteStoredWorkflowLiveBaseline(ctx context.Context, appID, name, alias, promotionID, actor, key string, request api.PromoteStoredWorkflowLiveBaselineRequest) (api.StoredWorkflowLiveBaseline, error) {
	policy, err := validateBaselinePromotion(alias, promotionID, actor, key, &request)
	if err != nil {
		return api.StoredWorkflowLiveBaseline{}, err
	}
	promotionID = uuid.MustParse(promotionID).String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowLiveBaseline{}, err
	}
	if _, ok := m.storedWorkflows[appID+"/"+name]; !ok {
		return api.StoredWorkflowLiveBaseline{}, ErrNotFound
	}
	count, total := 0, 0
	for _, records := range m.storedWorkflowLiveBaselines {
		for _, receipt := range records {
			if receipt.PromotionID == promotionID {
				if receipt.AppID != appID || receipt.Name != name {
					return api.StoredWorkflowLiveBaseline{}, ErrNotFound
				}
				if !baselineReplay(receipt, appID, name, alias, actor, key, request, policy) {
					return api.StoredWorkflowLiveBaseline{}, ErrStoredWorkflowLiveBaselineConflict
				}
				return cloneBaseline(receipt), nil
			}
		}
		if len(records) > 0 && records[0].AppID == appID && records[0].Name == name {
			total++
			if _, retired := m.storedWorkflowLiveBaselineRetirements[baselineKey(appID, name, records[0].BaselineName)]; !retired {
				count++
			}
		}
	}
	mapKey := baselineKey(appID, name, alias)
	if _, retired := m.storedWorkflowLiveBaselineRetirements[mapKey]; retired {
		return api.StoredWorkflowLiveBaseline{}, baselineRetiredError()
	}
	records := m.storedWorkflowLiveBaselines[mapKey]
	if len(records) == 0 && total >= api.StoredWorkflowLiveBaselineLifetimeNameLimit {
		return api.StoredWorkflowLiveBaseline{}, baselineLifetimeLimitError(int64(total + 1))
	}
	var prior api.StoredWorkflowLiveBaseline
	if len(records) > 0 {
		prior = records[len(records)-1]
	} else if count >= api.StoredWorkflowLiveBaselineLimit {
		return api.StoredWorkflowLiveBaseline{}, baselineLimitError(int64(count + 1))
	}
	if prior.Revision != request.ExpectedRevision {
		return api.StoredWorkflowLiveBaseline{}, ErrStoredWorkflowLiveBaselineConflict
	}
	suite, ok := m.storedWorkflowLiveSuites[request.SuiteID]
	if !ok || suite.AppID != appID || suite.Name != name {
		return api.StoredWorkflowLiveBaseline{}, ErrNotFound
	}
	view, err := m.liveSuiteViewLocked(suite)
	if err != nil {
		return api.StoredWorkflowLiveBaseline{}, err
	}
	receipt, err := promotedBaseline(appID, name, alias, promotionID, actor, key, request, policy, prior, view)
	if err != nil {
		return receipt, err
	}
	if m.storedWorkflowLiveBaselines == nil {
		m.storedWorkflowLiveBaselines = map[string][]api.StoredWorkflowLiveBaseline{}
	}
	records = append(records, receipt)
	if len(records) > api.StoredWorkflowLiveBaselineHistoryLimit {
		records = records[len(records)-api.StoredWorkflowLiveBaselineHistoryLimit:]
	}
	m.storedWorkflowLiveBaselines[mapKey] = records
	return cloneBaseline(receipt), nil
}
func (m *MemStore) liveSuitePinnedLocked(id string) bool {
	for key, records := range m.storedWorkflowLiveBaselines {
		if _, retired := m.storedWorkflowLiveBaselineRetirements[key]; retired {
			continue
		}
		if len(records) > 0 && records[len(records)-1].Suite.ID == id {
			return true
		}
	}
	return false
}

func (m *MemStore) clearStoredWorkflowLiveBaselinesLocked(appID, name string) {
	for key, records := range m.storedWorkflowLiveBaselines {
		if len(records) > 0 && records[0].AppID == appID && records[0].Name == name {
			delete(m.storedWorkflowLiveBaselines, key)
			delete(m.storedWorkflowLiveBaselineRetirements, key)
		}
	}
}
