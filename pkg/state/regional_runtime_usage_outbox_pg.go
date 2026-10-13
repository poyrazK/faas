package state

// adr: 802
import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RegionalRuntimeUsageOutboxStore = (*PgStore)(nil)

func regionalUsageOutboxParams(u regionalRuntimeUsageQueued) sqlc.RegionalRuntimeUsageOutboxParams {
	return sqlc.RegionalRuntimeUsageOutboxParams{AppID: uuidToPgtype(u.usage.Grant.Binding.RegionalAppID), AllocationID: uuidToPgtype(u.usage.Grant.AllocationID), Minute: pgtype.Timestamptz{Time: time.UnixMicro(u.usage.MinuteMicros).UTC(), Valid: true}}
}

func regionalUsageOutboxRow(row sqlc.RegionalRuntimeUsageOutbox) (RegionalRuntimeUsageOutboxItem, error) {
	r := RegionalRuntimeUsageOutboxItem{ID: row.ID, CreatedAt: row.CreatedAt.Time}
	for _, v := range []struct {
		raw []byte
		to  any
	}{{row.UsageClaims, &r.Usage}, {row.Envelope, &r.Envelope}, {row.AckClaims, &r.Ack}, {row.AckEnvelope, &r.AckEnvelope}} {
		if len(v.raw) > 0 && json.Unmarshal(v.raw, v.to) != nil {
			return RegionalRuntimeUsageOutboxItem{}, ErrConflict
		}
	}
	if r.Usage.Validate() != nil || r.Usage.Grant.Binding.RegionalAppID != pgUUIDString(row.AppID) || r.Usage.Grant.AllocationID != pgUUIDString(row.AllocationID) || r.Usage.Grant.Binding.CellID != pgUUIDString(row.CellID) || r.Usage.MinuteMicros != row.Minute.Time.UnixMicro() || (r.Ack == nil) != (r.AckEnvelope == nil) || (r.Ack != nil && (r.Ack.Validate() != nil || r.Ack.Usage != r.Usage)) {
		return RegionalRuntimeUsageOutboxItem{}, ErrConflict
	}
	return r, nil
}

func (s *PgStore) queueRegionalRuntimeUsage(ctx context.Context, change regionalRuntimeUsageQueued) (RegionalRuntimeUsageOutboxItem, error) {
	key := regionalUsageOutboxParams(change)
	return withRegionalCapacityTx(ctx, s, change.usage.Grant.Binding.RegionalAppID, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeUsageOutboxItem, error) {
		origin, err := readRegionalQuota(ctx, tx, change.usage.Grant.Binding.RegionalAppID, change.usage.Grant.AllocationID)
		if err != nil {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		if err = checkRegionalUsageOrigin(change.usage, origin); err != nil {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		old, err := q.RegionalRuntimeUsageOutbox(ctx, tx, key)
		if err == nil {
			r, err := regionalUsageOutboxRow(old)
			if err == nil && r.Usage != change.usage {
				err = ErrConflict
			}
			return r, err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		claims, err := json.Marshal(change.usage)
		if err != nil {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		envelope, err := json.Marshal(change.envelope)
		if err != nil {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		row, err := q.InsertRegionalRuntimeUsageOutbox(ctx, tx, sqlc.InsertRegionalRuntimeUsageOutboxParams{AppID: key.AppID, AllocationID: key.AllocationID, CellID: uuidToPgtype(change.usage.Grant.Binding.CellID), Minute: key.Minute, UsageClaims: claims, Envelope: envelope})
		if err != nil {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		return regionalUsageOutboxRow(row)
	})
}

func (s *PgStore) ackRegionalRuntimeUsage(ctx context.Context, change regionalRuntimeUsageImported) (RegionalRuntimeUsageOutboxItem, error) {
	key := regionalUsageOutboxParams(regionalRuntimeUsageQueued{usage: change.ack.Usage})
	return withRegionalCapacityTx(ctx, s, change.ack.Usage.Grant.Binding.RegionalAppID, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeUsageOutboxItem, error) {
		row, err := q.RegionalRuntimeUsageOutbox(ctx, tx, key)
		if err != nil {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		old, err := regionalUsageOutboxRow(row)
		if err != nil {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		if old.Usage != change.ack.Usage || (old.Ack != nil && *old.Ack != change.ack) {
			return RegionalRuntimeUsageOutboxItem{}, ErrConflict
		}
		if old.Ack != nil {
			return old, nil
		}
		claims, err := json.Marshal(change.ack)
		if err != nil {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		envelope, err := json.Marshal(change.envelope)
		if err != nil {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		row, err = q.AckRegionalRuntimeUsageOutbox(ctx, tx, sqlc.AckRegionalRuntimeUsageOutboxParams{AppID: key.AppID, AllocationID: key.AllocationID, Minute: key.Minute, AckClaims: claims, AckEnvelope: envelope})
		if err != nil {
			return RegionalRuntimeUsageOutboxItem{}, err
		}
		return regionalUsageOutboxRow(row)
	})
}

func (s *PgStore) RegionalRuntimeUsageOutbox(ctx context.Context, app, allocation string, minute time.Time) (RegionalRuntimeUsageOutboxItem, error) {
	if !validRegionalBillingHistoryPage(app, minute, 0, 1) || !validRegionalReleaseIDs(allocation, allocation, allocation) {
		return RegionalRuntimeUsageOutboxItem{}, ErrInvalidArgument
	}
	row, err := (&sqlc.Queries{}).RegionalRuntimeUsageOutbox(ctx, s.pool, sqlc.RegionalRuntimeUsageOutboxParams{AppID: uuidToPgtype(app), AllocationID: uuidToPgtype(allocation), Minute: pgtype.Timestamptz{Time: minute.UTC(), Valid: true}})
	if err != nil {
		return RegionalRuntimeUsageOutboxItem{}, regionalCapacityError(err)
	}
	return regionalUsageOutboxRow(row)
}

func (s *PgStore) ListPendingRegionalRuntimeUsage(ctx context.Context, cell string, after int64, limit int) ([]RegionalRuntimeUsageOutboxItem, error) {
	if !validRegionalUsagePage(cell, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := (&sqlc.Queries{}).ListPendingRegionalRuntimeUsage(ctx, s.pool, sqlc.ListPendingRegionalRuntimeUsageParams{CellID: uuidToPgtype(cell), AfterID: after, BatchLimit: int32(limit)})
	if err != nil {
		return nil, regionalCapacityError(err)
	}
	out := make([]RegionalRuntimeUsageOutboxItem, 0, len(rows))
	for _, row := range rows {
		r, err := regionalUsageOutboxRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
