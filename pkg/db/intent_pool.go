package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IntentPool carries a request's membership/credential locks through every
// catalogue commit. Background workers without a fence retain ordinary behavior.
type IntentPool struct{ Pool *pgxpool.Pool }

func NewIntentPool(pool *pgxpool.Pool) *IntentPool { return &IntentPool{Pool: pool} }
func rollbackIntent(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}
func (p *IntentPool) Begin(ctx context.Context) (pgx.Tx, error) {
	return p.BeginTx(ctx, pgx.TxOptions{})
}
func (p *IntentPool) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	tx, err := p.Pool.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	// Read-only transactions cannot write intent or take row locks.
	if opts.AccessMode != pgx.ReadOnly {
		if err = checkIntentFence(ctx, tx); err != nil {
			rollbackIntent(ctx, tx)
			return nil, err
		}
	}
	return tx, nil
}

func (p *IntentPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if !hasIntentFence(ctx) {
		return p.Pool.Exec(ctx, sql, args...)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer rollbackIntent(ctx, tx)
	tag, err := tx.Exec(ctx, sql, args...)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return tag, err
}

func (p *IntentPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if !hasIntentFence(ctx) {
		return p.Pool.QueryRow(ctx, sql, args...)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return intentRow{err: err}
	}
	return intentRow{row: tx.QueryRow(ctx, sql, args...), tx: tx, ctx: ctx}
}

type intentRow struct {
	row pgx.Row
	tx  pgx.Tx
	ctx context.Context
	err error
}

func (r intentRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer rollbackIntent(r.ctx, r.tx)
	if err := r.row.Scan(dest...); err != nil {
		return err
	}
	return r.tx.Commit(r.ctx)
}

func (p *IntentPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if !hasIntentFence(ctx) {
		return p.Pool.Query(ctx, sql, args...)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		rollbackIntent(ctx, tx)
		return nil, err
	}
	return &intentRows{Rows: rows, tx: tx, ctx: ctx}, nil
}

type intentRows struct {
	pgx.Rows
	tx     pgx.Tx
	ctx    context.Context
	closed bool
	err    error
}

func (r *intentRows) Close() {
	if r.closed {
		return
	}
	r.closed = true
	r.Rows.Close()
	if r.Rows.Err() != nil {
		r.err = r.Rows.Err()
		rollbackIntent(r.ctx, r.tx)
		return
	}
	r.err = r.tx.Commit(r.ctx)
}

func (r *intentRows) Next() bool {
	if r.Rows.Next() {
		return true
	}
	r.Close()
	return false
}

func (r *intentRows) Err() error {
	if r.err != nil {
		return r.err
	}
	return r.Rows.Err()
}
