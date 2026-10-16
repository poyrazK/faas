package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) RecordOperatorAlertSnapshot(ctx context.Context, input OperatorAlertSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := normalizeOperatorAlertSnapshot(input)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !s.ObservedAt.After(m.operatorAlertCollection.LastAttemptAt) {
		return nil
	}
	if m.operatorAlertEpisodes == nil {
		m.operatorAlertEpisodes = map[string]OperatorAlertEpisode{}
		m.operatorAlertRules = map[string]OperatorAlertRuleObservation{}
	}
	accepted := map[string]OperatorAlertRuleObservation{}
	for _, r := range s.Rules {
		previous, ok := m.operatorAlertRules[r.Key]
		if ok && (r.EvaluatedAt.Before(previous.EvaluatedAt) || r.EvaluatedAt.Equal(previous.EvaluatedAt) && !slices.Equal(r.EpisodeIDs, previous.EpisodeIDs)) {
			continue
		}
		m.operatorAlertRules[r.Key] = r
		accepted[r.Key] = r
	}
	for id, e := range m.operatorAlertEpisodes {
		r, ok := accepted[e.RuleKey]
		if ok && e.ResolvedAt == nil && !e.LastEvaluatedAt.After(r.EvaluatedAt) && !slices.Contains(r.EpisodeIDs, id) {
			at := r.EvaluatedAt
			e.ResolvedAt = &at
			e.LastEvaluatedAt = at
			e.LastCheckedAt = s.ObservedAt
			m.operatorAlertEpisodes[id] = e
		}
	}
	for _, e := range s.Episodes {
		r, ok := accepted[e.RuleKey]
		if !ok {
			continue
		}
		if old, ok := m.operatorAlertEpisodes[e.ID]; ok {
			if old.ResolvedAt != nil {
				continue
			}
			old.LastSeenAt = s.ObservedAt
			old.LastCheckedAt = s.ObservedAt
			old.LastEvaluatedAt = r.EvaluatedAt
			old.RunbookURL = e.RunbookURL
			m.operatorAlertEpisodes[e.ID] = old
		} else {
			e.Triage = OperatorAlertTriage{}
			e.FirstSeenAt = s.ObservedAt
			e.LastSeenAt = s.ObservedAt
			e.LastCheckedAt = s.ObservedAt
			e.LastEvaluatedAt = r.EvaluatedAt
			m.operatorAlertEpisodes[e.ID] = e
		}
	}
	m.operatorAlertCollection = OperatorAlertCollection{s.ObservedAt, s.ObservedAt}
	m.operatorAlertCollectionOutcomes = append(m.operatorAlertCollectionOutcomes, OperatorAlertCollectionOutcome{AttemptedAt: s.ObservedAt, Outcome: "success"})
	return nil
}

func (m *MemStore) RecordOperatorAlertFailure(ctx context.Context, at time.Time, reasons ...string) error {
	reason, err := collectionFailureReason(reasons)
	if err != nil {
		return err
	}
	return m.recordOperatorAlertFailure(ctx, at, reason, nil)
}

func (m *MemStore) RecordOperatorAlertRejection(ctx context.Context, at time.Time, d api.ObsAlertCollectionRejection) error {
	encoded, err := encodeOperatorAlertRejection(at, d)
	if err != nil {
		return err
	}
	copy, err := decodeOperatorAlertRejection(at, encoded)
	if err != nil {
		return err
	}
	return m.recordOperatorAlertFailure(ctx, at, "snapshot_rejected", copy)
}

func (m *MemStore) recordOperatorAlertFailure(ctx context.Context, at time.Time, reason string, d *api.ObsAlertCollectionRejection) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if at.IsZero() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	at = at.UTC().Truncate(time.Microsecond)
	if at.After(m.operatorAlertCollection.LastAttemptAt) {
		m.operatorAlertCollection.LastAttemptAt = at
		m.operatorAlertCollectionOutcomes = append(m.operatorAlertCollectionOutcomes, OperatorAlertCollectionOutcome{AttemptedAt: at, Outcome: "failure", Reason: reason})
		if d != nil {
			if m.operatorAlertCollectionDiagnostics == nil {
				m.operatorAlertCollectionDiagnostics = map[time.Time]api.ObsAlertCollectionRejection{}
			}
			m.operatorAlertCollectionDiagnostics[at] = *d
		}
	}
	return nil
}

func (m *MemStore) OperatorAlertCollectionStatus(ctx context.Context) (OperatorAlertCollection, error) {
	if err := ctx.Err(); err != nil {
		return OperatorAlertCollection{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.operatorAlertCollection, nil
}

func (m *MemStore) OperatorAlertEpisodes(ctx context.Context, f OperatorAlertFilter) (OperatorAlertPage, error) {
	if err := ctx.Err(); err != nil {
		return OperatorAlertPage{}, err
	}
	if err := validateOperatorAlertFilter(f); err != nil {
		return OperatorAlertPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := OperatorAlertPage{Episodes: []OperatorAlertEpisode{}, Collection: m.operatorAlertCollection}
	for _, e := range m.operatorAlertEpisodes {
		at := e.ObservedAt()
		if !matchOperatorAlertTriage(e.Triage, f) {
			continue
		}
		if e.ResolvedAt != nil && at.Before(f.Since) || f.Severity != "" && e.Severity != f.Severity || f.Status != "" && e.Status(out.Collection, f.Now) != f.Status {
			continue
		}
		if f.BeforeID != "" && (at.After(f.BeforeAt) || at.Equal(f.BeforeAt) && "platform_alert/"+e.ID >= f.BeforeID) {
			continue
		}
		out.Episodes = append(out.Episodes, copyOperatorAlertEpisode(e))
	}
	sort.Slice(out.Episodes, func(i, j int) bool {
		a, b := out.Episodes[i], out.Episodes[j]
		if a.ObservedAt().Equal(b.ObservedAt()) {
			return a.ID > b.ID
		}
		return a.ObservedAt().After(b.ObservedAt())
	})
	if len(out.Episodes) > f.Limit {
		out.Episodes = out.Episodes[:f.Limit]
	}
	return out, nil
}

func (m *MemStore) OperatorAlertEpisode(ctx context.Context, id string) (OperatorAlertEpisode, OperatorAlertCollection, error) {
	if err := ctx.Err(); err != nil {
		return OperatorAlertEpisode{}, OperatorAlertCollection{}, err
	}
	if !operatorAlertHash.MatchString(id) {
		return OperatorAlertEpisode{}, OperatorAlertCollection{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.operatorAlertEpisodeLocked(id)
}

func (m *MemStore) operatorAlertEpisodeLocked(id string) (OperatorAlertEpisode, OperatorAlertCollection, error) {
	if e, ok := m.operatorAlertEpisodes[id]; ok {
		return copyOperatorAlertEpisode(e), m.operatorAlertCollection, nil
	}
	var best OperatorAlertEpisode
	for _, e := range m.operatorAlertEpisodes {
		if e.SeriesID == id && (best.ID == "" || e.StartedAt.After(best.StartedAt) || e.StartedAt.Equal(best.StartedAt) && (e.FirstSeenAt.After(best.FirstSeenAt) || e.FirstSeenAt.Equal(best.FirstSeenAt) && e.ID > best.ID)) {
			best = e
		}
	}
	if best.ID == "" {
		return best, m.operatorAlertCollection, ErrNotFound
	}
	return copyOperatorAlertEpisode(best), m.operatorAlertCollection, nil
}

func (m *MemStore) PruneOperatorAlertHistory(ctx context.Context, before time.Time, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if before.IsZero() || limit < 1 || limit > api.ObsAlertHistoryPruneBatch {
		return 0, ErrInvalidArgument
	}
	started := time.Now().UTC().Truncate(time.Microsecond)
	before = before.UTC().Truncate(time.Microsecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	type candidate struct {
		id string
		at time.Time
	}
	rows := []candidate{}
	for id, e := range m.operatorAlertEpisodes {
		at := e.LastSeenAt
		if e.ResolvedAt != nil {
			at = *e.ResolvedAt
		}
		if at.Before(before) {
			rows = append(rows, candidate{id, at})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].at.Equal(rows[j].at) {
			return rows[i].id < rows[j].id
		}
		return rows[i].at.Before(rows[j].at)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	for _, r := range rows {
		delete(m.operatorAlertEpisodes, r.id)
	}
	used := map[string]bool{}
	for _, e := range m.operatorAlertEpisodes {
		used[e.RuleKey] = true
	}
	keys := []string{}
	for key, r := range m.operatorAlertRules {
		if !used[key] && r.EvaluatedAt.Before(before) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := m.operatorAlertRules[keys[i]], m.operatorAlertRules[keys[j]]
		if a.EvaluatedAt.Equal(b.EvaluatedAt) {
			return keys[i] < keys[j]
		}
		return a.EvaluatedAt.Before(b.EvaluatedAt)
	})
	if len(keys) > limit {
		keys = keys[:limit]
	}
	for _, key := range keys {
		delete(m.operatorAlertRules, key)
	}
	m.pruneAlertCollectionOutcomesLocked(before)
	if started.After(m.operatorAlertRetention.LastAttemptAt) {
		m.operatorAlertRetention.LastAttemptAt = started
		m.operatorAlertRetention.LastSuccessAt = started
		m.operatorAlertRetention.LastPrunedEpisodes = int64(len(rows))
	}
	return len(rows), nil
}

func copyOperatorAlertEpisode(e OperatorAlertEpisode) OperatorAlertEpisode {
	e.Triage = copyOperatorAlertTriage(e.Triage)
	if e.ResolvedAt != nil {
		at := *e.ResolvedAt
		e.ResolvedAt = &at
	}
	return e
}
