package state

// adr: 631
import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) BeginRegionalRuntimeRetirement(ctx context.Context, p runtimequota.CapacityPermit, fence string) (RegionalRuntimeCapacityReceipt, error) {
	if p.Validate() != nil || !validRegionalReleaseIDs(fence, fence, fence) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	app := p.Grant.Binding.RegionalAppID
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeCapacityReceipt, error) {
		old, err := readRegionalCapacity(ctx, tx, app, p.ID)
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		r, err := beginRegionalRetirement(old, p, fence)
		if err != nil || old.FenceID != "" {
			return r, err
		}
		row, err := q.FenceRegionalRuntimeCapacity(ctx, tx, sqlc.FenceRegionalRuntimeCapacityParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(p.ID), FenceID: uuidToPgtype(r.FenceID)})
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		return regionalCapacityRow(row)
	})
}

func (s *PgStore) completeRegionalRuntimeRetirement(ctx context.Context, stop runtimequota.CapacityStop, signer runtimequota.Signer, cells runtimequota.Trust) (RegionalRuntimeCapacityReceipt, error) {
	app := stop.Permit.Grant.Binding.RegionalAppID
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeCapacityReceipt, error) {
		old, err := readRegionalCapacity(ctx, tx, app, stop.Permit.ID)
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		r, err := completeRegionalRetirement(old, stop, signer, cells)
		if err != nil || old.Stop != nil {
			return r, err
		}
		claims, err := json.Marshal(r.Stop)
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		envelope, err := json.Marshal(r.StopEnvelope)
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		row, err := q.StopRegionalRuntimeCapacity(ctx, tx, sqlc.StopRegionalRuntimeCapacityParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(stop.Permit.ID), FenceID: uuidToPgtype(stop.FenceID), StopClaims: claims, StopEnvelope: envelope})
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		return regionalCapacityRow(row)
	})
}
