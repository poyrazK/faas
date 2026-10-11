package state

// adr: 798
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ GlobalRuntimeUsageStore = (*PgStore)(nil)

func globalRuntimeUsageError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	var constraint *pgconn.PgError
	if errors.As(err, &constraint) && (constraint.Code == "23505" || constraint.Code == "23514") {
		err = ErrConflict
	}
	return fmt.Errorf("state: global runtime usage: %w", err)
}

func globalRuntimeUsageRow(row sqlc.GlobalRuntimeUsageMinute) (GlobalRuntimeUsageReceipt, error) {
	r := GlobalRuntimeUsageReceipt{AccountID: pgUUIDString(row.AccountID), AppSlug: row.AppSlug, BillingInstanceID: pgUUIDString(row.BillingInstanceID), ImportedAt: row.ImportedAt.Time}
	if err := json.Unmarshal(row.UsageClaims, &r.Usage); err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	if err := json.Unmarshal(row.Envelope, &r.Envelope); err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	if r.Usage.Validate() != nil || r.BillingInstanceID != globalRuntimeBillingID(r.Usage.Grant) || r.Usage.MinuteMicros != row.Minute.Time.UnixMicro() ||
		r.Usage.Grant.GlobalAppID != pgUUIDString(row.GlobalAppID) || r.Usage.Grant.AllocationID != pgUUIDString(row.AllocationID) || r.Usage.Grant.Binding.CellID != pgUUIDString(row.CellID) || r.AccountID != r.Usage.Grant.OwnerID {
		return GlobalRuntimeUsageReceipt{}, ErrConflict
	}
	return r, nil
}

func (s *PgStore) applyGlobalRuntimeUsage(ctx context.Context, change globalRuntimeUsageMutation) (GlobalRuntimeUsageReceipt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	if err := q.AssertGlobalDirectoryScope(ctx, tx); err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	g := change.usage.Grant
	bound, err := q.ReadGlobalRuntimeUsageBinding(ctx, tx, sqlc.ReadGlobalRuntimeUsageBindingParams{GlobalAppID: uuidToPgtype(g.GlobalAppID), AllocationID: uuidToPgtype(g.AllocationID), CellID: g.Binding.CellID})
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	b := GlobalRuntimeUsageBinding{AccountID: bound.AccountID, AppSlug: bound.AppSlug}
	if bound.ReleasedAt.Valid {
		v := bound.ReleasedAt.Time
		b.ReleasedAt = &v
	}
	if err := json.Unmarshal(bound.GrantClaims, &b.Grant); err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	if err := checkGlobalRuntimeUsageBinding(change.usage, b); err != nil {
		return GlobalRuntimeUsageReceipt{}, err
	}
	if err := q.FinancialEvidenceAccountLock(ctx, tx, uuidToPgtype(b.AccountID)); err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	minute := pgtype.Timestamptz{Time: time.UnixMicro(change.usage.MinuteMicros).UTC(), Valid: true}
	key := sqlc.ReadGlobalRuntimeUsageParams{GlobalAppID: uuidToPgtype(g.GlobalAppID), AllocationID: uuidToPgtype(g.AllocationID), CellID: uuidToPgtype(g.Binding.CellID), Minute: minute}
	old, err := q.ReadGlobalRuntimeUsage(ctx, tx, key)
	if err == nil {
		r, err := globalRuntimeUsageRow(old)
		if err != nil {
			return GlobalRuntimeUsageReceipt{}, err
		}
		if r.Usage != change.usage {
			return GlobalRuntimeUsageReceipt{}, ErrConflict
		}
		return r, globalRuntimeUsageError(tx.Commit(ctx))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	return s.insertGlobalRuntimeUsage(ctx, tx, change, b, key)
}

func (s *PgStore) insertGlobalRuntimeUsage(ctx context.Context, tx pgx.Tx, change globalRuntimeUsageMutation, b GlobalRuntimeUsageBinding, key sqlc.ReadGlobalRuntimeUsageParams) (GlobalRuntimeUsageReceipt, error) {
	usage, err := json.Marshal(change.usage)
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	envelope, err := json.Marshal(change.envelope)
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	q := &sqlc.Queries{}
	err = q.InsertGlobalRuntimeUsage(ctx, tx, sqlc.InsertGlobalRuntimeUsageParams{GlobalAppID: key.GlobalAppID, AllocationID: key.AllocationID, CellID: key.CellID, Minute: key.Minute, AccountID: uuidToPgtype(b.AccountID), AppSlug: b.AppSlug, BillingInstanceID: uuidToPgtype(globalRuntimeBillingID(b.Grant)), UsageClaims: usage, Envelope: envelope})
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	inserted, err := q.InsertGlobalRuntimeCanonicalUsage(ctx, tx, sqlc.InsertGlobalRuntimeCanonicalUsageParams(key))
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	if inserted != 1 {
		return GlobalRuntimeUsageReceipt{}, ErrConflict
	}
	row, err := q.ReadGlobalRuntimeUsage(ctx, tx, key)
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	r, err := globalRuntimeUsageRow(row)
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, err
	}
	return r, globalRuntimeUsageError(tx.Commit(ctx))
}

func (s *PgStore) GlobalRuntimeUsage(ctx context.Context, app, allocation, cell string, minute time.Time) (GlobalRuntimeUsageReceipt, error) {
	if !validRegionalReleaseIDs(app, allocation, cell) || !minute.Equal(minute.Truncate(time.Minute)) || minute.UnixMicro() < 1 {
		return GlobalRuntimeUsageReceipt{}, ErrInvalidArgument
	}
	row, err := (&sqlc.Queries{}).ReadGlobalRuntimeUsage(ctx, s.pool, sqlc.ReadGlobalRuntimeUsageParams{GlobalAppID: uuidToPgtype(app), AllocationID: uuidToPgtype(allocation), CellID: uuidToPgtype(cell), Minute: pgtype.Timestamptz{Time: minute.UTC(), Valid: true}})
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, globalRuntimeUsageError(err)
	}
	return globalRuntimeUsageRow(row)
}

func (s *PgStore) GlobalRuntimeBillingReady(ctx context.Context, account string, start, end time.Time) (bool, error) {
	if !validFinancialWindow(account, start, end) || !start.Equal(start.Truncate(time.Minute)) || !end.Equal(end.Truncate(time.Minute)) {
		return false, ErrInvalidArgument
	}
	ready, err := (&sqlc.Queries{}).GlobalRuntimeBillingReady(ctx, s.pool, sqlc.GlobalRuntimeBillingReadyParams{AccountID: uuidToPgtype(account), WindowStart: pgtype.Timestamptz{Time: start.UTC(), Valid: true}, WindowEnd: pgtype.Timestamptz{Time: end.UTC(), Valid: true}})
	return ready, globalRuntimeUsageError(err)
}
