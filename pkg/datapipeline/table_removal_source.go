package datapipeline

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func liveRemovalRelations(p Pipeline, remaining bool) []relation {
	relations := p.SchemaChange.Before
	if remaining {
		relations = slices.DeleteFunc(slices.Clone(relations), func(r relation) bool {
			return r.Name == p.SchemaChange.Cancellation.Table
		})
	}
	return relations
}

// Before retirement the complete approved baseline must match. After retirement
// begins, the retained table no longer needs a source definition; every remaining
// table must still match, including source RLS and replica-identity eligibility.
func checkLiveRemovalSource(ctx context.Context, p Pipeline, source sourceMetadata, remaining bool) error {
	for _, before := range liveRemovalRelations(p, remaining) {
		actual, table, err := inspectSourceTable(ctx, source, before.Name)
		if err != nil {
			return err
		}
		if !table.Supported || !sameVerificationRelation(actual, before) {
			return &blocked{"source_schema_changed", true}
		}
	}
	return nil
}

func lockLiveRemovalSource(ctx context.Context, p Pipeline, source *pgxpool.Pool, remaining bool) (pgx.Tx, error) {
	tx, err := lockVerificationSource(ctx, source, relationNames(liveRemovalRelations(p, remaining)))
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "42P01" {
			return nil, &blocked{"source_schema_changed", true}
		}
		return nil, err
	}
	if err = checkLiveRemovalSource(ctx, p, tx, remaining); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func liveRemovalFence(source pgx.Tx, current func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := current(ctx); err != nil {
			return err
		}
		// Detect an abandoned source transaction before committing the target.
		var held int
		return source.QueryRow(ctx, "SELECT 1").Scan(&held)
	}
}

func beginLiveRemoval(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, current func(context.Context) error) error {
	locked, err := lockLiveRemovalSource(ctx, p, source, false)
	if err != nil {
		return err
	}
	defer func() { _ = locked.Rollback(ctx) }()
	if err = checkRecordedSourceStorage(ctx, p, locked, target, liveRemovalRelations(p, false)); err != nil {
		return err
	}
	return beginTableCancellation(ctx, p, target, liveRemovalFence(locked, current))
}

func completeLiveRemoval(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, result *SchemaChange, stage string, current func(context.Context) error) error {
	locked, err := lockLiveRemovalSource(ctx, p, source, true)
	if err != nil {
		return err
	}
	defer func() { _ = locked.Rollback(ctx) }()
	if err = checkRecordedSourceStorage(ctx, p, locked, target, liveRemovalRelations(p, true)); err != nil {
		return err
	}
	return completeTableCancellation(ctx, p, target, result, stage, liveRemovalFence(locked, current))
}
