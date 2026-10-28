package state

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// workloadOOMFailedKind mirrors events.InstanceWorkloadOOMFailed; pkg/state
// cannot import pkg/events.
const workloadOOMFailedKind = "instances.workload_oom_failed"

// WorkloadOOMKillLister reads an app's OOM kill events for the debugger's
// status summary (ADR-121 and ADR-1447 amendments). It is optional so narrow
// test stores need not implement it; a store without it reports no kills.
type WorkloadOOMKillLister interface {
	ListAppWorkloadOOMKills(ctx context.Context, arg sqlc.ListAppWorkloadOOMKillsParams) ([]sqlc.ListAppWorkloadOOMKillsRow, error)
}

// ListAppWorkloadOOMKills lists the app's OOM kills in [Since, Until], newest
// first, each row carrying the window's total.
func (s *PgStore) ListAppWorkloadOOMKills(ctx context.Context, arg sqlc.ListAppWorkloadOOMKillsParams) ([]sqlc.ListAppWorkloadOOMKillsRow, error) {
	return sqlc.New().ListAppWorkloadOOMKills(ctx, s.pool, arg)
}

// ListAppWorkloadOOMKills is the in-memory equivalent of the PgStore query.
func (m *MemStore) ListAppWorkloadOOMKills(_ context.Context, arg sqlc.ListAppWorkloadOOMKillsParams) ([]sqlc.ListAppWorkloadOOMKillsRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []Event
	for _, e := range m.events {
		if e.Kind != workloadOOMFailedKind || e.At.Before(arg.Since.Time) || e.At.After(arg.Until.Time) {
			continue
		}
		var data struct {
			AppID string `json:"app_id"`
		}
		if json.Unmarshal(e.Data, &data) == nil && data.AppID == arg.AppID {
			matched = append(matched, e)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if !matched[i].At.Equal(matched[j].At) {
			return matched[i].At.After(matched[j].At)
		}
		return matched[i].ID > matched[j].ID
	})
	out := []sqlc.ListAppWorkloadOOMKillsRow{}
	for _, e := range matched {
		if len(out) == int(arg.RowLimit) {
			break
		}
		out = append(out, sqlc.ListAppWorkloadOOMKillsRow{
			At: pgtype.Timestamptz{Time: e.At, Valid: true}, Data: append([]byte(nil), e.Data...), Total: int64(len(matched)),
		})
	}
	return out, nil
}
