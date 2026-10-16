package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) OperatorAlertCollectionHistory(ctx context.Context, input OperatorAlertCollectionHistoryFilter) (OperatorAlertCollectionHistorySnapshot, error) {
	out := OperatorAlertCollectionHistorySnapshot{Outcomes: []OperatorAlertCollectionOutcome{}}
	f, err := normalizeCollectionHistoryFilter(input)
	if err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out.Collection = m.operatorAlertCollection
	out.Outcomes, err = m.operatorAlertCollectionOutcomesLocked(f.Since, f.Now)
	if err != nil {
		return out, err
	}
	return out, ctx.Err()
}

func (m *MemStore) operatorAlertCollectionOutcomesLocked(since, now time.Time) ([]OperatorAlertCollectionOutcome, error) {
	out := []OperatorAlertCollectionOutcome{}
	from := collectionOutcomeReadSince(since, now)
	for _, receipt := range m.operatorAlertCollectionOutcomes {
		if receipt.AttemptedAt.Before(from) || receipt.AttemptedAt.After(now) {
			continue
		}
		if len(out) >= api.ObsAlertCollectionHistoryOutcomesMax {
			return nil, &OperatorAlertCollectionHistoryLimitError{Limit: api.ObsAlertCollectionHistoryOutcomesMax}
		}
		out = append(out, receipt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AttemptedAt.Before(out[j].AttemptedAt) })
	return out, nil
}

func (m *MemStore) pruneAlertCollectionOutcomesLocked(before time.Time) {
	count := 0
	for count < len(m.operatorAlertCollectionOutcomes) && count < api.ObsAlertCollectionHistoryPruneBatch && m.operatorAlertCollectionOutcomes[count].AttemptedAt.Before(before) {
		count++
	}
	// Drop the old backing array so expired receipts do not stay resident.
	if count > 0 {
		m.operatorAlertCollectionOutcomes = append([]OperatorAlertCollectionOutcome{}, m.operatorAlertCollectionOutcomes[count:]...)
	}
}
