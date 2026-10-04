package state

// adr: 492
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RegionalRuntimeCapacityLedger = (*PgStore)(nil)

func (s *PgStore) CheckRegionalRuntimeIssuedPlacement(ctx context.Context, p runtimequota.Placement) error {
	if !validIssuedRegionalPlacement(p) {
		return ErrInvalidArgument
	}
	_, err := withRegionalCapacityTx(ctx, s, p.Permit.Grant.Binding.RegionalAppID, func(tx pgx.Tx, q *sqlc.Queries) (struct{}, error) {
		if err := checkCurrentRegionalCapacity(ctx, tx, q, p.Permit); err != nil {
			return struct{}{}, err
		}
		physical, err := readRegionalCapacity(ctx, tx, p.Permit.Grant.Binding.RegionalAppID, p.Permit.ID)
		if errors.Is(err, ErrNotFound) {
			err = ErrRegionalRuntimePlacementUnavailable
		}
		if err == nil {
			err = checkIssuedRegionalPhysical(p.Permit, physical)
		}
		if err != nil || p.Seat == nil {
			return struct{}{}, err
		}
		seat, err := readRegionalServingSeat(ctx, tx, p.Permit.Grant.Binding.RegionalAppID, p.Seat.ID)
		if errors.Is(err, ErrNotFound) {
			err = ErrRegionalRuntimePlacementUnavailable
		}
		if err == nil {
			err = checkIssuedRegionalSeat(*p.Seat, seat)
		}
		return struct{}{}, err
	})
	return err
}

func regionalCapacityError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch {
		case p.ConstraintName == "regional_capacity_exhausted":
			err = ErrRegionalRuntimeCapacity
		case p.ConstraintName == "regional_capacity_frozen":
			err = ErrRegionalRuntimeQuotaFrozen
		case p.ConstraintName == "regional_capacity_identity", p.Code == "23505":
			err = ErrConflict
		case p.Code == "23514":
			err = ErrInvalidArgument
		}
	}
	return fmt.Errorf("state: regional runtime capacity: %w", err)
}

// Take the same app lock as the signed quota journal, before reading any state.
// Read committed makes each post-lock statement observe a waiting retirement.
func withRegionalCapacityTx[T any](ctx context.Context, s *PgStore, app string, fn func(pgx.Tx, *sqlc.Queries) (T, error)) (T, error) {
	var zero T
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return zero, regionalCapacityError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	if err = q.LockRegionalRuntimeQuotaJournal(ctx, tx, uuidToPgtype(app)); err != nil {
		return zero, regionalCapacityError(err)
	}
	result, err := fn(tx, q)
	if err != nil {
		return zero, regionalCapacityError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, regionalCapacityError(err)
	}
	return result, nil
}

func checkCurrentRegionalCapacity(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, p runtimequota.CapacityPermit) error {
	receipt, err := readRegionalQuota(ctx, tx, p.Grant.Binding.RegionalAppID, p.Grant.AllocationID)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	head, err := readRegionalQuotaHead(ctx, tx, p.Grant.Binding.RegionalAppID)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if err = reserveRegionalCapacityCheck(p, receipt, head); err != nil {
		return err
	}
	b := p.Grant.Binding
	if _, err = q.LockRegionalRuntimeQuotaApp(ctx, tx, sqlc.LockRegionalRuntimeQuotaAppParams{AppID: uuidToPgtype(b.RegionalAppID), AccountID: uuidToPgtype(b.RegionalAccountID), OrgID: uuidToPgtype(b.RegionalOrgID), Slug: b.RegionalAppSlug}); err != nil {
		return regionalCapacityError(err)
	}
	if b.RegionalOrgID != "" {
		_, err = q.LockRegionalRuntimeQuotaTenant(ctx, tx, uuidToPgtype(b.RegionalOrgID))
	}
	return regionalCapacityError(err)
}

func regionalCapacityRow(row sqlc.RegionalRuntimeCapacityReceipt) (RegionalRuntimeCapacityReceipt, error) {
	r := RegionalRuntimeCapacityReceipt{CreatedAt: row.CreatedAt.Time, IssuedAt: exclusiveTime(row.IssuedAt), ReleasedAt: exclusiveTime(row.ReleasedAt), FenceID: exclusiveUUID(row.FenceID)}
	for _, f := range []struct {
		raw   []byte
		value any
	}{{row.PermitClaims, &r.Permit}, {row.StopClaims, &r.Stop}, {row.StopEnvelope, &r.StopEnvelope}} {
		if len(f.raw) != 0 {
			if err := json.Unmarshal(f.raw, f.value); err != nil {
				return RegionalRuntimeCapacityReceipt{}, regionalCapacityError(err)
			}
		}
	}
	return r, nil
}

func readRegionalCapacity(ctx context.Context, db sqlc.DBTX, app, id string) (RegionalRuntimeCapacityReceipt, error) {
	row, err := (&sqlc.Queries{}).RegionalRuntimeCapacity(ctx, db, sqlc.RegionalRuntimeCapacityParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(id)})
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, regionalCapacityError(err)
	}
	return regionalCapacityRow(row)
}

func (s *PgStore) RegionalRuntimeCapacity(ctx context.Context, app, id string) (RegionalRuntimeCapacityReceipt, error) {
	if !validRegionalReleaseIDs(app, id, id) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	return readRegionalCapacity(ctx, s.pool, app, id)
}

func (s *PgStore) ListRegionalRuntimeCapacity(ctx context.Context, app, allocation, after string, limit int) ([]RegionalRuntimeCapacityReceipt, error) {
	if !validCapacityPage(app, allocation, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := (&sqlc.Queries{}).ListRegionalRuntimeCapacity(ctx, s.pool, sqlc.ListRegionalRuntimeCapacityParams{AppID: uuidToPgtype(app), AllocationID: uuidToPgtype(allocation), AfterID: uuidToPgtype(after), RowLimit: int32(limit)})
	if err != nil {
		return nil, regionalCapacityError(err)
	}
	out := make([]RegionalRuntimeCapacityReceipt, 0, len(rows))
	for _, row := range rows {
		r, err := regionalCapacityRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *PgStore) RegionalRuntimeCapacityUsage(ctx context.Context, app, allocation string) (RegionalRuntimeCapacityUsage, error) {
	if !validRegionalReleaseIDs(app, allocation, allocation) {
		return RegionalRuntimeCapacityUsage{}, ErrInvalidArgument
	}
	row, err := (&sqlc.Queries{}).RegionalRuntimeCapacityUsage(ctx, s.pool, sqlc.RegionalRuntimeCapacityUsageParams{AppID: uuidToPgtype(app), AllocationID: uuidToPgtype(allocation)})
	return RegionalRuntimeCapacityUsage{TotalHeld: row.TotalHeld, IssuedHeld: row.IssuedHeld}, regionalCapacityError(err)
}

func (s *PgStore) ReserveRegionalRuntimeCapacity(ctx context.Context, p runtimequota.CapacityPermit) (RegionalRuntimeCapacityReceipt, error) {
	if p.Validate() != nil {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	return withRegionalCapacityTx(ctx, s, p.Grant.Binding.RegionalAppID, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeCapacityReceipt, error) {
		r, err := readRegionalCapacity(ctx, tx, p.Grant.Binding.RegionalAppID, p.ID)
		if err == nil {
			if r.Permit != p {
				return RegionalRuntimeCapacityReceipt{}, ErrConflict
			}
			return r, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		if err = checkCurrentRegionalCapacity(ctx, tx, q, p); err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		return insertRegionalCapacity(ctx, tx, q, p)
	})
}

func (s *PgStore) IssueRegionalRuntimeCapacity(ctx context.Context, app, id string) (RegionalRuntimeCapacityReceipt, error) {
	if !validRegionalReleaseIDs(app, id, id) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeCapacityReceipt, error) {
		r, err := readRegionalCapacity(ctx, tx, app, id)
		if err != nil || r.IssuedAt != nil {
			return r, err
		}
		if r.FenceID != "" || r.ReleasedAt != nil {
			return RegionalRuntimeCapacityReceipt{}, ErrConflict
		}
		if err = checkCurrentRegionalCapacity(ctx, tx, q, r.Permit); err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		row, err := q.IssueRegionalRuntimeCapacity(ctx, tx, sqlc.IssueRegionalRuntimeCapacityParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(id)})
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		return regionalCapacityRow(row)
	})
}

func (s *PgStore) FenceRegionalRuntimeCapacity(ctx context.Context, app, id, fenceID string) (RegionalRuntimeCapacityReceipt, error) {
	if !validRegionalReleaseIDs(app, id, fenceID) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeCapacityReceipt, error) {
		r, err := readRegionalCapacity(ctx, tx, app, id)
		if err != nil {
			return r, err
		}
		if _, err = fenceRegionalCapacity(r, fenceID); err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		if r.FenceID != "" {
			return r, nil
		}
		row, err := q.FenceRegionalRuntimeCapacity(ctx, tx, sqlc.FenceRegionalRuntimeCapacityParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(id), FenceID: uuidToPgtype(fenceID)})
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		return regionalCapacityRow(row)
	})
}

func (s *PgStore) applyRegionalRuntimeCapacityStop(ctx context.Context, stop runtimequota.CapacityStop, e runtimequota.Envelope) (RegionalRuntimeCapacityReceipt, error) {
	app := stop.Permit.Grant.Binding.RegionalAppID
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeCapacityReceipt, error) {
		old, err := readRegionalCapacity(ctx, tx, app, stop.Permit.ID)
		if err != nil {
			return old, err
		}
		r, err := stopRegionalCapacity(old, stop, e)
		if err != nil || old.Stop != nil {
			return r, err
		}
		claims, err := json.Marshal(stop)
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		envelope, err := json.Marshal(e)
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

func insertRegionalCapacity(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, p runtimequota.CapacityPermit) (RegionalRuntimeCapacityReceipt, error) {
	claims, err := json.Marshal(p)
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	row, err := q.InsertRegionalRuntimeCapacity(ctx, tx, sqlc.InsertRegionalRuntimeCapacityParams{AppID: uuidToPgtype(p.Grant.Binding.RegionalAppID), AllocationID: uuidToPgtype(p.Grant.AllocationID), PermitID: uuidToPgtype(p.ID), Generation: p.Grant.Generation, InstanceID: uuidToPgtype(p.InstanceID), RunID: uuidToPgtype(p.RunID), DeploymentID: uuidToPgtype(p.DeploymentID), NodeID: uuidToPgtype(p.NodeID), HostEpochID: uuidToPgtype(p.HostEpochID), Kind: p.Kind, PermitClaims: claims})
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	return regionalCapacityRow(row)
}
