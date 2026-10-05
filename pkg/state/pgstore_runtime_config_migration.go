package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeConfigMigrationStore = (*PgStore)(nil)

func (s *PgStore) MigrateInstanceOwnerWithRuntimeConfig(ctx context.Context, id, from, to, leaseToken string, input RuntimeConfigMigration) error {
	if err := validateRuntimeConfigMigration(input); err != nil {
		return err
	}
	if id == "" || from == "" || to == "" || leaseToken == "" {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := reserveNodeForMigration(ctx, tx, id, to); err != nil {
		return err
	}
	scope := ""
	if input.Inputs != nil {
		scope = input.Inputs.Scope
	}
	expectedWake := pgtype.UUID{}
	if input.ExpectedWakeID != "" {
		expectedWake = mustPgUUID(input.ExpectedWakeID)
	}
	q := sqlc.New()
	row, err := q.MigrateInstanceRuntimeConfig(ctx, tx, sqlc.MigrateInstanceRuntimeConfigParams{
		InstanceID: mustPgUUID(id), FromNodeID: mustPgUUID(from), ToNodeID: mustPgUUID(to), LeaseToken: leaseToken,
		ExpectedWakeID: expectedWake, CheckSourceWake: true, WakeID: mustPgUUID(input.WakeID), Scope: scope, HasInputs: input.Inputs != nil,
		Netns: input.Netns, HostIp: input.HostIP, GuestUid: int32(input.GuestUID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapErr(err)
	}
	if err := q.ClearInstanceRuntimeConfigReceipt(ctx, tx, row.ID); err != nil {
		return mapErr(err)
	}
	if input.Inputs != nil {
		if err := recordInstanceRuntimeConfigReceipt(ctx, tx, id, input.WakeID, *input.Inputs); err != nil {
			return err
		}
	}
	if err := q.StampRuntimeMigrationApp(ctx, tx, row.AppID); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}
