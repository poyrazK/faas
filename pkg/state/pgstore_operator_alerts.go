package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) RecordOperatorAlertSnapshot(ctx context.Context, input OperatorAlertSnapshot) error {
	snapshot, err := normalizeOperatorAlertSnapshot(input)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("alert history transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	collection, err := q.OperatorAlertCollectionLock(ctx, tx)
	if err != nil {
		return fmt.Errorf("alert history lock: %w", err)
	}
	if collection.LastAttemptAt.Valid && !snapshot.ObservedAt.After(collection.LastAttemptAt.Time) {
		return tx.Commit(ctx)
	}
	encoded, err := json.Marshal(snapshot.Rules)
	if err != nil {
		return fmt.Errorf("alert rules encoding: %w", err)
	}
	keys, err := q.OperatorAlertAdvanceRules(ctx, tx, encoded)
	if err != nil {
		return fmt.Errorf("alert rule observations: %w", err)
	}
	accepted := map[string]bool{}
	for _, key := range keys {
		accepted[key] = true
	}
	rules := []OperatorAlertRuleObservation{}
	evaluations := map[string]time.Time{}
	for _, rule := range snapshot.Rules {
		if accepted[rule.Key] {
			rules = append(rules, rule)
			evaluations[rule.Key] = rule.EvaluatedAt
		}
	}
	episodes := []OperatorAlertEpisode{}
	for _, e := range snapshot.Episodes {
		if accepted[e.RuleKey] {
			e.LastEvaluatedAt = evaluations[e.RuleKey]
			episodes = append(episodes, e)
		}
	}
	encodedRules, err := json.Marshal(rules)
	if err != nil {
		return fmt.Errorf("accepted alert rules encoding: %w", err)
	}
	encodedEpisodes, err := json.Marshal(episodes)
	if err != nil {
		return fmt.Errorf("alert episodes encoding: %w", err)
	}
	at := pgtype.Timestamptz{Time: snapshot.ObservedAt, Valid: true}
	if err := q.OperatorAlertResolveMissing(ctx, tx, sqlc.OperatorAlertResolveMissingParams{ObservedAt: at, Rules: encodedRules}); err != nil {
		return fmt.Errorf("resolve retained alerts: %w", err)
	}
	if err := q.OperatorAlertUpsertEpisodes(ctx, tx, sqlc.OperatorAlertUpsertEpisodesParams{ObservedAt: at, Episodes: encodedEpisodes}); err != nil {
		return fmt.Errorf("retain alert episodes: %w", err)
	}
	if err := q.OperatorAlertCollectionSucceeded(ctx, tx, at); err != nil {
		return fmt.Errorf("alert collection success: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PgStore) RecordOperatorAlertFailure(ctx context.Context, at time.Time) error {
	if at.IsZero() {
		return ErrInvalidArgument
	}
	if err := sqlc.New().OperatorAlertCollectionFailed(ctx, s.pool, pgtype.Timestamptz{Time: at.UTC().Truncate(time.Microsecond), Valid: true}); err != nil {
		return fmt.Errorf("alert collection failure: %w", err)
	}
	return nil
}

func (s *PgStore) OperatorAlertCollectionStatus(ctx context.Context) (OperatorAlertCollection, error) {
	row, err := sqlc.New().OperatorAlertCollection(ctx, s.pool)
	if err != nil {
		return OperatorAlertCollection{}, fmt.Errorf("alert collection status: %w", err)
	}
	return pgAlertCollection(row), nil
}

func (s *PgStore) OperatorAlertEpisodes(ctx context.Context, f OperatorAlertFilter) (OperatorAlertPage, error) {
	out := OperatorAlertPage{Episodes: []OperatorAlertEpisode{}}
	if err := validateOperatorAlertFilter(f); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("alert history read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	collection, err := q.OperatorAlertCollection(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("alert collection read: %w", err)
	}
	out.Collection = pgAlertCollection(collection)
	rows, err := q.OperatorAlertEpisodes(ctx, tx, sqlc.OperatorAlertEpisodesParams{
		Status: f.Status, Severity: f.Severity, PageLimit: int32(f.Limit), BeforeID: f.BeforeID,
		BeforeAt: pgtype.Timestamptz{Time: f.BeforeAt, Valid: !f.BeforeAt.IsZero()}, Since: pgtype.Timestamptz{Time: f.Since, Valid: true},
		FreshSince: pgtype.Timestamptz{Time: f.Now.Add(-api.ObsHealthFreshnessMaxAge), Valid: true}, FutureBefore: pgtype.Timestamptz{Time: f.Now.Add(api.ObsHealthClockSkewTolerance), Valid: true},
	})
	if err != nil {
		return out, fmt.Errorf("alert episode list: %w", err)
	}
	for _, row := range rows {
		out.Episodes = append(out.Episodes, pgAlertEpisode(sqlc.OperatorAlertEpisodeRow(row)))
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) OperatorAlertEpisode(ctx context.Context, id string) (OperatorAlertEpisode, OperatorAlertCollection, error) {
	if !operatorAlertHash.MatchString(id) {
		return OperatorAlertEpisode{}, OperatorAlertCollection{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return OperatorAlertEpisode{}, OperatorAlertCollection{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	collection, err := q.OperatorAlertCollection(ctx, tx)
	if err != nil {
		return OperatorAlertEpisode{}, OperatorAlertCollection{}, fmt.Errorf("alert collection read: %w", err)
	}
	row, err := q.OperatorAlertEpisode(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorAlertEpisode{}, pgAlertCollection(collection), ErrNotFound
	}
	if err != nil {
		return OperatorAlertEpisode{}, pgAlertCollection(collection), fmt.Errorf("alert evidence read: %w", err)
	}
	return pgAlertEpisode(row), pgAlertCollection(collection), tx.Commit(ctx)
}

func (s *PgStore) PruneOperatorAlertHistory(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit < 1 || limit > api.ObsAlertHistoryPruneBatch {
		return 0, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Use the collector's lock order so cleanup cannot race episode capture.
	if _, err := q.OperatorAlertCollectionLock(ctx, tx); err != nil {
		return 0, err
	}
	count, err := q.OperatorAlertPruneEpisodes(ctx, tx, sqlc.OperatorAlertPruneEpisodesParams{Before: pgtype.Timestamptz{Time: before, Valid: true}, BatchSize: int32(limit)})
	if err != nil {
		return 0, fmt.Errorf("prune alert episodes: %w", err)
	}
	if err := q.OperatorAlertPruneRules(ctx, tx, sqlc.OperatorAlertPruneRulesParams{Before: pgtype.Timestamptz{Time: before, Valid: true}, BatchSize: int32(limit)}); err != nil {
		return 0, fmt.Errorf("prune alert rules: %w", err)
	}
	return int(count), tx.Commit(ctx)
}

func pgAlertCollection(row sqlc.OperatorAlertCollectionRow) OperatorAlertCollection {
	var out OperatorAlertCollection
	if row.LastAttemptAt.Valid {
		out.LastAttemptAt = row.LastAttemptAt.Time.UTC()
	}
	if row.LastSuccessAt.Valid {
		out.LastSuccessAt = row.LastSuccessAt.Time.UTC()
	}
	return out
}

func pgAlertEpisode(r sqlc.OperatorAlertEpisodeRow) OperatorAlertEpisode {
	out := OperatorAlertEpisode{ID: r.ID, SeriesID: r.SeriesID, RuleKey: r.RuleKey, Name: r.Name, Component: r.Component, Severity: r.Severity,
		AccountID: r.AccountID, AppID: r.AppID, NodeID: r.NodeID, RunbookURL: r.RunbookUrl,
		StartedAt: r.StartedAt.Time.UTC(), FirstSeenAt: r.FirstSeenAt.Time.UTC(), LastSeenAt: r.LastSeenAt.Time.UTC(), LastEvaluatedAt: r.LastEvaluatedAt.Time.UTC(), LastCheckedAt: r.LastCheckedAt.Time.UTC()}
	if r.ResolvedAt.Valid {
		at := r.ResolvedAt.Time.UTC()
		out.ResolvedAt = &at
	}
	return out
}
