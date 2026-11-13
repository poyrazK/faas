// Package dataapibudget coordinates opt-in owner request budgets across replicas.
package dataapibudget

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/dataapibudget/sqlc"
)

var ErrNotFound = errors.New("data api budget: app not found")

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type Decision struct {
	Allowed    bool `json:"allowed"`
	RetryAfter int  `json:"retry_after_seconds"`
}

// Take spends a request before customer SQL. A lost response spends the credit
// conservatively; callers never retry this operation automatically.
func (s *Store) Take(ctx context.Context, account, app, key string, limit, seconds int) (Decision, error) {
	if limit < 1 || limit > api.DataAPISharedBudgetMaxRequests || seconds < 1 || seconds > api.DataAPISharedBudgetMaxWindowSeconds || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(key) {
		return Decision{}, errors.New("invalid shared budget")
	}
	ctx, cancel := context.WithTimeout(ctx, api.DataAPISharedBudgetTimeout)
	defer cancel()
	result := Decision{}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		if _, err := q.LockApp(ctx, tx, sqlc.LockAppParams{AppID: app, AccountID: account}); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		at, err := q.DatabaseTime(ctx, tx)
		if err != nil {
			return err
		}
		start := at.Time.UTC().Truncate(time.Duration(seconds) * time.Second)
		end := start.Add(time.Duration(seconds) * time.Second)
		result.RetryAfter = int(math.Ceil(end.Sub(at.Time).Seconds()))
		if result.RetryAfter < 1 {
			result.RetryAfter = 1
		}
		windowStart := pgtype.Timestamptz{Time: start, Valid: true}
		windowEnd := pgtype.Timestamptz{Time: end, Valid: true}
		// Existing keys lock only their own counter. Capacity serialization and
		// pruning are needed when registering a new key, not on every request.
		_, err = q.FindKey(ctx, tx, sqlc.FindKeyParams{AppID: app, BudgetKey: key})
		if err == nil {
			_, err = q.ConsumeExisting(ctx, tx, sqlc.ConsumeExistingParams{AppID: app, BudgetKey: key, WindowStart: windowStart, WindowEnd: windowEnd, RequestLimit: int64(limit)})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			result.Allowed = err == nil
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err = q.LockCapacity(ctx, tx, app); err != nil {
			return err
		}
		if err = q.Prune(ctx, tx, sqlc.PruneParams{AppID: app, Now: at, BatchSize: api.DataAPIUsagePruneBatchLimit}); err != nil {
			return err
		}
		_, err = q.Consume(ctx, tx, sqlc.ConsumeParams{AppID: app, BudgetKey: key, WindowStart: windowStart, WindowEnd: windowEnd, MaxKeys: api.DataAPISharedBudgetMaxKeys, RequestLimit: int64(limit)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		result.Allowed = err == nil
		return err
	})
	if err != nil {
		return Decision{}, fmt.Errorf("shared budget admission: %w", err)
	}
	return result, nil
}
