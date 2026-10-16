package state

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
)

func (m *MemStore) OperatorAlertResponseTimes(ctx context.Context, input OperatorAlertResponseTimesFilter) (OperatorAlertResponseTimesSnapshot, error) {
	out := OperatorAlertResponseTimesSnapshot{Episodes: []OperatorAlertResponseTimesEpisode{}}
	f, err := normalizeAlertResponseTimesFilter(input)
	if err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out.Collection = m.operatorAlertCollection
	out.Retention = m.operatorAlertRetentionStatusLocked(f.Now)
	out.CollectionOutcomes, err = m.operatorAlertCollectionOutcomesLocked(f.Since, f.Now)
	if err != nil {
		return out, err
	}
	if out.Collection.LastSuccessAt.IsZero() {
		return out, nil
	}
	ids := map[string]bool{}
	for _, e := range m.operatorAlertEpisodes {
		if e.StartedAt.Before(f.Since) || e.StartedAt.After(f.Now) {
			continue
		}
		if len(out.Episodes) >= api.ObsAlertResponseTimesEpisodesMax {
			return out, &OperatorAlertResponseTimesLimitError{Resource: "episodes", Limit: api.ObsAlertResponseTimesEpisodesMax}
		}
		status := e.Status(out.Collection, f.Now)
		if !f.CollectionEnabled && status != "resolved" {
			status = "unknown"
		}
		e = copyOperatorAlertEpisode(e)
		out.Episodes = append(out.Episodes, OperatorAlertResponseTimesEpisode{ID: e.ID, Severity: e.Severity, Component: e.Component, Status: status, StartedAt: e.StartedAt, ResolvedAt: e.ResolvedAt, Revision: e.Triage.Revision})
		ids[e.ID] = true
	}
	sort.Slice(out.Episodes, func(i, j int) bool {
		a, b := out.Episodes[i], out.Episodes[j]
		if a.StartedAt.Equal(b.StartedAt) {
			return a.ID > b.ID
		}
		return a.StartedAt.After(b.StartedAt)
	})
	receipts := []alertTimingReceipt{}
	for _, r := range m.auditLog {
		if r.Kind != "operator.action.alert_triage" {
			continue
		}
		var header struct {
			EpisodeID string `json:"episode_id"`
		}
		if err := json.Unmarshal(r.Data, &header); err != nil || !ids[header.EpisodeID] {
			continue
		}
		if len(receipts) >= api.ObsAlertResponseTimesReceiptsMax {
			return out, &OperatorAlertResponseTimesLimitError{Resource: "receipts", Limit: api.ObsAlertResponseTimesReceiptsMax}
		}
		receipts = append(receipts, alertTimingReceipt{EpisodeID: header.EpisodeID, ID: r.ID.String(), At: r.ReceivedAt, Payload: r.Data})
	}
	if err := applyAlertTimingReceipts(out.Episodes, receipts, f.Now); err != nil {
		return out, err
	}
	return out, ctx.Err()
}
