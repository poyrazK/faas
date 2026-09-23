package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// InstanceReadiness is the latest reversible readiness transition observed
// for a live instance. It is an optional Store capability used by gateway
// cache hydration; readiness is deliberately not folded into instance state.
type InstanceReadiness struct {
	Ready   bool
	At      time.Time
	EventID int64
}

// InstanceReadinessReader is a narrow optional state.Store capability so
// existing Store implementations and test doubles need not grow their base
// interface solely for gateway hydration.
type InstanceReadinessReader interface {
	LatestInstanceReadiness(ctx context.Context, instanceIDs []string) (map[string]InstanceReadiness, error)
}

func (s *PgStore) LatestInstanceReadiness(ctx context.Context, instanceIDs []string) (map[string]InstanceReadiness, error) {
	out := make(map[string]InstanceReadiness)
	if len(instanceIDs) == 0 {
		return out, nil
	}
	rows, err := sqlc.New().LatestInstanceReadiness(ctx, s.pool, instanceIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.InstanceID == "" || !row.At.Valid || (row.Status != "ready" && row.Status != "unready") {
			continue
		}
		out[row.InstanceID] = InstanceReadiness{Ready: row.Status == "ready", At: row.At.Time, EventID: row.ID}
	}
	return out, nil
}

func (m *MemStore) LatestInstanceReadiness(_ context.Context, instanceIDs []string) (map[string]InstanceReadiness, error) {
	out := make(map[string]InstanceReadiness)
	if len(instanceIDs) == 0 {
		return out, nil
	}
	wanted := make(map[string]struct{}, len(instanceIDs))
	for _, id := range instanceIDs {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, event := range m.events {
		if event.Kind != "wake.sidecar_health" {
			continue
		}
		var payload struct {
			InstanceID string `json:"instance_id"`
			Status     string `json:"status"`
		}
		if json.Unmarshal(event.Data, &payload) != nil || (payload.Status != "ready" && payload.Status != "unready") {
			continue
		}
		if _, ok := wanted[payload.InstanceID]; !ok {
			continue
		}
		current, ok := out[payload.InstanceID]
		if !ok || event.At.After(current.At) || (event.At.Equal(current.At) && event.ID > current.EventID) {
			out[payload.InstanceID] = InstanceReadiness{Ready: payload.Status == "ready", At: event.At, EventID: event.ID}
		}
	}
	return out, nil
}
