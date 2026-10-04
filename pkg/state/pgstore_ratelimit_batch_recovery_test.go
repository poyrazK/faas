//go:build !no_pg

// adr: 570 (ADR-104 consult coalescing)
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

func TestPGRateLimitBackendBatchRecoveryPreservesDebt(t *testing.T) {
	for _, failure := range []string{"pool", "missing-table", "row-lock"} {
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
			switch failure {
			case "pool":
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
				queryCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
				granted, _, err := state.NewPGRateLimitBackend(single).ConsumeTokens(queryCtx, "app", subject, "hobby", 1, 1, 2)
				cancel()
				if granted != 0 || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("blocked pool granted=%d err=%v", granted, err)
				}
				requireCentralBackoff(t, err, true)
			case "missing-table":
				if _, err := pool.Exec(ctx, "ALTER TABLE pg_ratelimit_counters RENAME TO pg_ratelimit_counters_offline"); err != nil {
					t.Fatal(err)
				}
				granted, _, err := backend.ConsumeTokens(ctx, "app", subject, "hobby", 1, 1, 2)
				if granted != 0 {
					t.Fatal("missing counter table admitted requests")
				}
				requireCentralBackoff(t, err, false)
				if _, err := pool.Exec(ctx, "ALTER TABLE pg_ratelimit_counters_offline RENAME TO pg_ratelimit_counters"); err != nil {
					t.Fatal(err)
				}
			case "row-lock":
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(context.Background()) }()
				if _, err := tx.Exec(ctx, "SELECT subject_id FROM pg_ratelimit_counters WHERE subject_id=$1 FOR UPDATE", subject); err != nil {
					t.Fatal(err)
				}
				queryCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
				granted, _, err := backend.ConsumeTokens(queryCtx, "app", subject, "hobby", 1, 1, 2)
				cancel()
				if granted != 0 || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("locked counter granted=%d err=%v", granted, err)
				}
				requireCentralBackoff(t, err, false)
				if err := tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if granted, remaining, err := backend.ConsumeTokens(ctx, "app", subject, "hobby", 1, 1, 2); err != nil || granted != 0 || remaining != 0 {
				t.Fatalf("recovered counter=(%d,%d,%v), want (0,0,nil)", granted, remaining, err)
			}
		})
	}
}

func TestPGRateLimitBackendBatchClockRollbackPreservesBalance(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	backend := state.NewPGRateLimitBackend(pool)
	subject := uuid.NewString()
	if remaining, admitted, err := backend.ConsumeToken(ctx, "app", subject, "hobby", 1, 3); err != nil || !admitted || remaining != 2 {
		t.Fatalf("seed counter=(%d,%v,%v)", remaining, admitted, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE pg_ratelimit_counters SET last_refill=now()+interval '1 minute' WHERE subject_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if granted, remaining, err := backend.ConsumeTokens(ctx, "app", subject, "hobby", 1, 3, 2); err != nil || granted != 2 || remaining != 0 {
		t.Fatalf("existing balance after clock rollback=(%d,%d,%v), want (2,0,nil)", granted, remaining, err)
	}
	if granted, remaining, err := backend.ConsumeTokens(ctx, "app", subject, "hobby", 1, 3, 2); err != nil || granted != 0 || remaining != 0 {
		t.Fatalf("clock rollback forgave debt=(%d,%d,%v), want (0,0,nil)", granted, remaining, err)
	}
}
