package state

import (
	"context"
	"sort"
)

func (m *MemStore) ListRuntimeReleases(_ context.Context, runtime, arch string) ([]RuntimeRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RuntimeRelease, 0)
	for _, r := range m.runtimeReleases {
		if r.Runtime == runtime && r.Architecture == arch {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > runtimeCatalogLimit() {
		out = out[:runtimeCatalogLimit()]
	}
	return out, nil
}
