package migrationprobe

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	probesql "github.com/onebox-faas/faas/pkg/e2etest/migrationprobe/sqlc"
)

var ErrPromotionCommand = errors.New("qualification command returned nonzero after expansion")

func (c Config) PromotionSessionName() string { return "gregale-promotion:" + c.RunID }

// InitializePromotion installs disposable customer data and a controllable
// command-result barrier. This is application SQL, never a platform receipt.
func (c Config) InitializePromotion(ctx context.Context, mode string, released bool) error {
	conn, err := c.migration(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if c.initialize(ctx, conn) != nil {
		return ErrProbe
	}
	return c.initializePromotion(ctx, conn, mode, released)
}

func (c Config) initializePromotion(ctx context.Context, conn *pgx.Conn, mode string, released bool) error {
	id, err := c.ID()
	if err != nil {
		return err
	}
	q := probesql.New()
	if q.InitializePromotion(ctx, conn) != nil || q.SeedPromotion(ctx, conn, probesql.SeedPromotionParams{RunID: id, Mode: mode, Released: released}) != nil {
		return ErrProbe
	}
	return nil
}

// Promote records every invocation before attempting DDL: a replay cannot hide
// behind a unique marker or an already-present column. Expansion commits before
// the result barrier so owner loss leaves a real ambiguous, durable SQL effect.
func (c Config) Promote(ctx context.Context) error {
	conn, err := c.migration(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return c.promote(ctx, conn)
}

func (c Config) promote(ctx context.Context, conn *pgx.Conn) error {
	q := probesql.New()
	id, err := c.ID()
	if err != nil {
		return err
	}
	if q.ContractSessionName(ctx, conn, c.PromotionSessionName()) != nil {
		return ErrProbe
	}
	attempt, err := q.BeginPromotion(ctx, conn, id)
	if err != nil || attempt.Executions != 1 {
		return ErrProbe
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return ErrProbe
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if q.ExpandColumn(ctx, tx) != nil || q.BackfillCurrent(ctx, tx) != nil || q.RequireCurrent(ctx, tx) != nil || q.ApplyPromotion(ctx, tx, id) != nil || tx.Commit(ctx) != nil {
		return ErrProbe
	}
	if attempt.Mode == "failure" {
		return ErrPromotionCommand
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := q.ObservePromotion(ctx, conn, id)
		if err != nil {
			return ErrProbe
		}
		if state.Mode == "success" && state.Released {
			return nil
		}
		select {
		case <-ctx.Done():
			return ErrProbe
		case <-ticker.C:
		}
	}
}
