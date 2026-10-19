package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) OperatorAlertRecurrence(ctx context.Context, input OperatorAlertRecurrenceFilter) (OperatorAlertRecurrenceSnapshot, error) {
	out := OperatorAlertRecurrenceSnapshot{Episodes: []OperatorAlertRecurrenceEpisode{}}
	f, err := normalizeAlertRecurrenceFilter(input)
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
	for _, e := range m.operatorAlertEpisodes {
		if e.StartedAt.Before(f.Since) || e.StartedAt.After(f.Now) {
			continue
		}
		if len(out.Episodes) >= api.ObsAlertRecurrenceEpisodesMax {
			return out, &OperatorAlertRecurrenceLimitError{Limit: api.ObsAlertRecurrenceEpisodesMax}
		}
		status := e.Status(out.Collection, f.Now)
		if !f.CollectionEnabled && status != "resolved" {
			status = "unknown"
		}
		e = copyOperatorAlertEpisode(e)
		out.Episodes = append(out.Episodes, OperatorAlertRecurrenceEpisode{ID: e.ID, RuleKey: e.RuleKey, SeriesID: e.SeriesID, Name: e.Name, Severity: e.Severity, Component: e.Component, AccountID: e.AccountID, AppID: e.AppID, NodeID: e.NodeID, Status: status, StartedAt: e.StartedAt, ResolvedAt: e.ResolvedAt})
	}
	sort.Slice(out.Episodes, func(i, j int) bool {
		a, b := out.Episodes[i], out.Episodes[j]
		if a.StartedAt.Equal(b.StartedAt) {
			return a.ID > b.ID
		}
		return a.StartedAt.After(b.StartedAt)
	})
	return out, ctx.Err()
}
