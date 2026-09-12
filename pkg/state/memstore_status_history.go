package state

import (
	"context"
	"sort"
	"time"
)

// StatusUptimeBuckets is the MemStore mirror of the public status rollup.
func (m *MemStore) StatusUptimeBuckets(_ context.Context, since time.Time) ([]StatusUptimeBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return statusUptimeBuckets(m.invocations, since.UTC()), nil
}

// ListStatusIncidentsSince is the MemStore mirror of the public incident
// timeline. Open incidents older than the window remain visible so a long
// outage cannot disappear from the public page while it is still active.
func (m *MemStore) ListStatusIncidentsSince(_ context.Context, since time.Time, limit int) ([]StatusIncident, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]StatusIncident, 0, len(m.statusIncidents))
	for _, inc := range m.statusIncidents {
		if inc.PostedAt.Before(since) && inc.ResolvedAt != nil {
			continue
		}
		out = append(out, inc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PostedAt.Equal(out[j].PostedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].PostedAt.After(out[j].PostedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
