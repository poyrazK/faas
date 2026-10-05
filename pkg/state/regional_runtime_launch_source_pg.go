package state

// adr: 633
import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func regionalLaunchSourceRow(row sqlc.RegionalRuntimeLaunchSource) (RegionalRuntimeLaunchSource, error) {
	r := RegionalRuntimeLaunchSource{CreatedAt: row.CreatedAt.Time}
	r.Source.InstanceKey = row.InstanceKey
	if err := json.Unmarshal(row.PermitClaims, &r.Source.Permit); err != nil {
		return RegionalRuntimeLaunchSource{}, regionalCapacityError(err)
	}
	if r.Source.Validate() != nil {
		return RegionalRuntimeLaunchSource{}, ErrConflict
	}
	return r, nil
}

func readRegionalLaunchSource(ctx context.Context, db sqlc.DBTX, app, permit string) (RegionalRuntimeLaunchSource, error) {
	row, err := (&sqlc.Queries{}).RegionalRuntimeLaunchSource(ctx, db, sqlc.RegionalRuntimeLaunchSourceParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(permit)})
	if err != nil {
		return RegionalRuntimeLaunchSource{}, regionalCapacityError(err)
	}
	return regionalLaunchSourceRow(row)
}

func prepareRegionalLaunchSource(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, source runtimequota.LaunchSource, physical RegionalRuntimeCapacityReceipt) error {
	app := source.Permit.Grant.Binding.RegionalAppID
	old, err := readRegionalLaunchSource(ctx, tx, app, source.Permit.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := checkRegionalLaunchSource(source, old, physical); err != nil {
		return err
	}
	if old.Source.Permit.ID != "" {
		return nil
	}
	claims, err := json.Marshal(source.Permit)
	if err != nil {
		return err
	}
	_, err = q.InsertRegionalRuntimeLaunchSource(ctx, tx, sqlc.InsertRegionalRuntimeLaunchSourceParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(source.Permit.ID), InstanceKey: source.InstanceKey, PermitClaims: claims})
	return err
}

func (s *PgStore) RegionalRuntimeLaunchSource(ctx context.Context, app, permit string) (RegionalRuntimeLaunchSource, error) {
	if !validRegionalReleaseIDs(app, permit, permit) {
		return RegionalRuntimeLaunchSource{}, ErrInvalidArgument
	}
	return readRegionalLaunchSource(ctx, s.pool, app, permit)
}

func (s *PgStore) ListRegionalRuntimeRetirements(ctx context.Context, cell string, after RegionalRuntimeRetirementCursor, limit int) ([]RegionalRuntimeRetirementCandidate, error) {
	if !validRegionalRetirementPage(cell, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := (&sqlc.Queries{}).ListRegionalRuntimeRetirements(ctx, s.pool, sqlc.ListRegionalRuntimeRetirementsParams{CellID: cell, FirstPage: after.AppID == "", AfterApp: uuidToPgtype(after.AppID), AfterPermit: uuidToPgtype(after.PermitID), BatchLimit: int32(limit)})
	if err != nil {
		return nil, regionalCapacityError(err)
	}
	result := make([]RegionalRuntimeRetirementCandidate, 0, len(rows))
	for _, row := range rows {
		r := RegionalRuntimeRetirementCandidate{Source: runtimequota.LaunchSource{InstanceKey: row.InstanceKey}, FenceID: exclusiveUUID(row.FenceID)}
		if err := json.Unmarshal(row.PermitClaims, &r.Source.Permit); err != nil || r.Source.Validate() != nil || r.Source.Permit.Grant.Binding.CellID != cell ||
			(runtimequota.CapacityStop{Permit: r.Source.Permit, FenceID: r.FenceID, AdmissionFrozen: true}).Validate() != nil {
			return nil, ErrConflict
		}
		result = append(result, r)
	}
	return result, nil
}
