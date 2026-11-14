package datapipeline

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgreSQL can drop native table-sync slots without an associated main slot.
// Detachment and DROP share a transaction so a failed or lost reply cannot
// leave a subscription whose immutable identity no longer proves ownership.
func dropSubscriptionWithoutMainSlot(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, backed bool, current func(context.Context) error) error {
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	owned, err := checkSubscriptionOwnership(ctx, p, tx, backed)
	if err != nil {
		return err
	}
	if !owned {
		return ErrConflict
	}
	var subID uint32
	var systemID int64
	if err = tx.QueryRow(ctx, `SELECT s.oid,c.system_identifier FROM pg_subscription s CROSS JOIN pg_control_system() c WHERE s.subname=$1 AND s.subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND NOT s.subenabled`, pipelineName(p)).Scan(&subID, &systemID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(p))+" SET(slot_name=NONE)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "DROP SUBSCRIPTION "+ident(pipelineName(p))); err != nil {
		return err
	}
	// With slot_name=NONE, native DROP can silently tolerate an unavailable
	// publisher. Prove that no native sync slots remain before acknowledging it.
	// The subscriber system identifier prevents collisions with other clusters;
	// include all relation OIDs, including sync slots no longer in its catalog.
	pattern := "^pg_" + strconv.FormatUint(uint64(subID), 10) + "_sync_[0-9]+_" + strconv.FormatUint(uint64(systemID), 10) + "$"
	var remaining bool
	if err = source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE database=current_database() AND slot_name ~ $1)`, pattern).Scan(&remaining); err != nil {
		return err
	}
	if remaining {
		return ErrUnavailable
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
