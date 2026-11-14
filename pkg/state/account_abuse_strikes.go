package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	strikesql "github.com/onebox-faas/faas/pkg/state/abusestrikes/sqlc"
)

var (
	_ AccountAbuseStrikeStore = (*PgStore)(nil)
	_ AccountAbuseStrikeStore = (*MemStore)(nil)
)

// validAbuseStrikeReason reports whether reason is an automatic hold reason;
// an operator hold is placed directly and is never a strike.
func validAbuseStrikeReason(reason string) bool {
	return reason != AccountAbuseHoldOperator && validAbuseHoldReason(reason)
}

// RecordAccountAbuseStrike implements AccountAbuseStrikeStore. The insert and
// the count are separate statements: the count runs after this strike
// commits, so whichever of two concurrent strikes counts last sees both.
func (s *PgStore) RecordAccountAbuseStrike(ctx context.Context, accountID, reason, appID string, at time.Time, window time.Duration) (int, error) {
	if !validAbuseStrikeReason(reason) || window <= 0 {
		return 0, ErrInvalidArgument
	}
	owner := mustPgUUID(accountID)
	if !owner.Valid {
		return 0, ErrInvalidArgument
	}
	app := mustPgUUID(appID)
	if appID != "" && !app.Valid {
		return 0, ErrInvalidArgument
	}
	cutoff := pgtype.Timestamptz{Time: at.Add(-window).UTC(), Valid: true}
	q := strikesql.New()
	if err := q.PruneStrikes(ctx, s.pool, cutoff); err != nil {
		return 0, fmt.Errorf("state: prune abuse strikes: %w", mapErr(err))
	}
	if err := q.InsertStrike(ctx, s.pool, strikesql.InsertStrikeParams{
		AccountID: owner, Reason: reason, AppID: app,
		StruckAt: pgtype.Timestamptz{Time: at.UTC(), Valid: true},
	}); err != nil {
		return 0, fmt.Errorf("state: record abuse strike %s: %w", accountID, mapErr(err))
	}
	n, err := q.CountStrikes(ctx, s.pool, strikesql.CountStrikesParams{AccountID: owner, Cutoff: cutoff})
	if err != nil {
		return 0, fmt.Errorf("state: count abuse strikes %s: %w", accountID, mapErr(err))
	}
	return int(n), nil
}

// RecordAccountAbuseStrike implements AccountAbuseStrikeStore.
func (m *MemStore) RecordAccountAbuseStrike(_ context.Context, accountID, reason, _ string, at time.Time, window time.Duration) (int, error) {
	if !validAbuseStrikeReason(reason) || window <= 0 || accountID == "" {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.abuseStrikes == nil {
		m.abuseStrikes = make(map[string][]time.Time)
	}
	cutoff := at.Add(-window)
	kept := m.abuseStrikes[accountID][:0]
	for _, struck := range m.abuseStrikes[accountID] {
		if struck.After(cutoff) {
			kept = append(kept, struck)
		}
	}
	kept = append(kept, at)
	m.abuseStrikes[accountID] = kept
	return len(kept), nil
}
