// adr: 375
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PGTrafficRetryBackend shares the daemon pool; app rows and the database
// clock own the ten-second window across gateway replacements and replicas.
type PGTrafficRetryBackend struct{ pool *pgxpool.Pool }

func NewPGTrafficRetryBackend(pool *pgxpool.Pool) *PGTrafficRetryBackend {
	return &PGTrafficRetryBackend{pool: pool}
}

func (b *PGTrafficRetryBackend) ObserveOriginal(ctx context.Context, scope string, window time.Duration) error {
	id, err := uuid.Parse(scope)
	if err != nil {
		return fmt.Errorf("retry budget app: %w", err)
	}
	if window <= 0 {
		return errors.New("retry budget requires a positive window")
	}
	err = sqlc.New().ObserveTrafficRetryOriginal(ctx, b.pool, sqlc.ObserveTrafficRetryOriginalParams{
		AppID: pgtype.UUID{Bytes: id, Valid: true}, WindowMs: window.Milliseconds(),
	})
	if err != nil {
		return fmt.Errorf("observe shared retry original: %w", err)
	}
	return nil
}

func (b *PGTrafficRetryBackend) AllowRetry(ctx context.Context, scope string, percent, minimum int) (bool, error) {
	id, err := uuid.Parse(scope)
	if err != nil {
		return false, fmt.Errorf("retry budget app: %w", err)
	}
	if percent < 0 || minimum < 0 {
		return false, errors.New("negative retry allowance")
	}
	_, err = sqlc.New().AdmitTrafficRetry(ctx, b.pool, sqlc.AdmitTrafficRetryParams{
		AppID: pgtype.UUID{Bytes: id, Valid: true}, Percent: int64(percent), MinRetries: int64(minimum),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("spend shared retry: %w", err)
	}
	return true, nil
}

func (b *PGTrafficRetryBackend) BackendID() string {
	c := b.pool.Config().ConnConfig
	sum := sha256.Sum256([]byte(fmt.Sprintf("postgres:v1|%s|%d|%s", c.Host, c.Port, c.Database)))
	return hex.EncodeToString(sum[:8])
}

// Expired rows cannot grant retries and can be pruned safely by any replica.
func (b *PGTrafficRetryBackend) Prune(ctx context.Context) (int64, error) {
	n, err := sqlc.New().PruneTrafficRetryCounters(ctx, b.pool)
	if err != nil {
		return 0, fmt.Errorf("prune shared retry counters: %w", err)
	}
	return n, nil
}
