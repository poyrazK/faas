// adr: 570
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

func requireCentralBackoff(t *testing.T, err error, want bool) {
	t.Helper()
	var classified interface{ CanBackoffCentralConsult() bool }
	if err == nil || !errors.As(err, &classified) || classified.CanBackoffCentralConsult() != want {
		t.Fatalf("central error=%v, want classified backoff=%v", err, want)
	}
}

func TestPGRateLimitBackendClassifiesPoolAcquisitionFailure(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	config := pool.Config()
	config.MaxConns, config.MinConns = 1, 0
	single, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	held, err := single.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	backend := state.NewPGRateLimitBackend(single)
	queryCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()
	_, admitted, err := backend.ConsumeToken(queryCtx, "app", uuid.NewString(), "hobby", 1, 1)
	if admitted || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked pool admitted=%v err=%v", admitted, err)
	}
	requireCentralBackoff(t, err, true)
}

func TestPGRateLimitBackendStatementRecoveryPreservesDebt(t *testing.T) {
	for _, failure := range []string{"missing-table", "row-lock"} {
		t.Run(failure, func(t *testing.T) {
			_, pool, ctx := pgStoreWithPool(t)
			backend := state.NewPGRateLimitBackend(pool)
			subject := uuid.NewString()
			if _, admitted, err := backend.ConsumeToken(ctx, "app", subject, "hobby", 1, 1); err != nil || !admitted {
				t.Fatalf("seed counter admitted=%v err=%v", admitted, err)
			}
			if _, err := pool.Exec(ctx, "UPDATE pg_ratelimit_counters SET last_refill=now()+interval '1 minute' WHERE subject_id=$1", subject); err != nil {
				t.Fatal(err)
			}
			if failure == "missing-table" {
				if _, err := pool.Exec(ctx, "ALTER TABLE pg_ratelimit_counters RENAME TO pg_ratelimit_counters_offline"); err != nil {
					t.Fatal(err)
				}
				_, admitted, err := backend.ConsumeToken(ctx, "app", subject, "hobby", 1, 1)
				if admitted {
					t.Fatal("missing counter table admitted a request")
				}
				requireCentralBackoff(t, err, false)
				if _, err := pool.Exec(ctx, "ALTER TABLE pg_ratelimit_counters_offline RENAME TO pg_ratelimit_counters"); err != nil {
					t.Fatal(err)
				}
			} else {
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(context.Background()) }()
				if _, err := tx.Exec(ctx, "SELECT subject_id FROM pg_ratelimit_counters WHERE subject_id=$1 FOR UPDATE", subject); err != nil {
					t.Fatal(err)
				}
				queryCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
				_, admitted, err := backend.ConsumeToken(queryCtx, "app", subject, "hobby", 1, 1)
				cancel()
				if admitted || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("locked counter admitted=%v err=%v", admitted, err)
				}
				requireCentralBackoff(t, err, false)
				if err := tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if remaining, admitted, err := backend.ConsumeToken(ctx, "app", subject, "hobby", 1, 1); err != nil || admitted || remaining != 0 {
				t.Fatalf("recovered counter=(%d,%v,%v), want (0,false,nil)", remaining, admitted, err)
			}
		})
	}
}
