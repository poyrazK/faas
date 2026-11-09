// adr: 1304
package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) ListStoredWorkflowLiveBaselines(ctx context.Context, appID, name string) (api.ListStoredWorkflowLiveBaselinesResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := api.ListStoredWorkflowLiveBaselinesResponse{Baselines: []api.StoredWorkflowLiveBaselineHealth{}, ObservedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if _, ok := m.storedWorkflows[appID+"/"+name]; !ok {
		return out, ErrNotFound
	}
	for key, records := range m.storedWorkflowLiveBaselines {
		if _, retired := m.storedWorkflowLiveBaselineRetirements[key]; retired {
			continue
		}
		if len(records) == 0 {
			continue
		}
		head := records[len(records)-1]
		if head.AppID != appID || head.Name != name {
			continue
		}
		var view *api.StoredWorkflowLiveSuite
		if record, ok := m.storedWorkflowLiveSuites[head.Suite.ID]; ok && record.AppID == appID && record.Name == name {
			current, err := m.liveSuiteViewLocked(record)
			if err != nil {
				return out, err
			}
			view = &current
		}
		out.Baselines = append(out.Baselines, baselineHealth(head, view, out.ObservedAt))
	}
	sort.Slice(out.Baselines, func(i, j int) bool {
		return out.Baselines[i].Baseline.BaselineName < out.Baselines[j].Baseline.BaselineName
	})
	return out, nil
}
