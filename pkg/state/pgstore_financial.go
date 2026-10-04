package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FinancialStore = (*PgStore)(nil)

func financialTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func (s *PgStore) FinancialEvidenceHead(ctx context.Context, account string, start, end time.Time) (int64, error) {
	if !validFinancialWindow(account, start, end) {
		return 0, ErrInvalidArgument
	}
	id, err := parsePgUUID(account)
	if err != nil {
		return 0, ErrInvalidArgument
	}
	return sqlc.New().FinancialEvidenceHead(ctx, s.pool, sqlc.FinancialEvidenceHeadParams{AccountID: id, PeriodStart: financialTimestamp(start), PeriodEnd: financialTimestamp(end)})
}

func (s *PgStore) FinancialEvidenceCoverage(ctx context.Context) (time.Time, error) {
	row, err := sqlc.New().FinancialEvidenceCoverage(ctx, s.pool)
	if err != nil {
		return time.Time{}, fmt.Errorf("financial evidence coverage: %w", err)
	}
	return row.Time.UTC(), nil
}

func (s *PgStore) ListFinancialUsageEvidence(ctx context.Context, account string, start, end time.Time, after, through int64, limit int) ([]FinancialUsageRecord, error) {
	if !validFinancialWindow(account, start, end) || after < 0 || through < after || limit < 1 || limit > api.FinancialEvidencePageMax {
		return nil, ErrInvalidArgument
	}
	id, err := parsePgUUID(account)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().FinancialUsageEvidenceList(ctx, s.pool, sqlc.FinancialUsageEvidenceListParams{AccountID: id, PeriodStart: financialTimestamp(start), PeriodEnd: financialTimestamp(end), AfterID: after, ThroughID: through, PageSize: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("financial evidence list: %w", err)
	}
	out := make([]FinancialUsageRecord, 0, len(rows))
	for _, r := range rows {
		record, err := financialEvidenceFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func financialEvidenceFromRow(r sqlc.FinancialUsageEvidence) (FinancialUsageRecord, error) {
	a := financial.Attribution{}
	if err := json.Unmarshal(r.Attribution, &a); err != nil {
		return FinancialUsageRecord{}, fmt.Errorf("financial evidence attribution: %w", err)
	}
	return FinancialUsageRecord{Sequence: r.ID, InstanceID: pgUUIDString(r.InstanceID), Plan: api.Plan(r.Plan), Unit: r.Unit, PriceVersion: r.PriceVersion.String, AdjustmentActor: r.AdjustmentActor.String, AdjustmentReason: r.AdjustmentReason.String, Evidence: financial.Evidence{ID: strconv.FormatInt(r.ID, 10), AccountID: pgUUIDString(r.AccountID), SourceID: r.SourceID, CorrectsSourceID: r.CorrectsSourceID.String, Meter: r.Meter, Quantity: r.Quantity, Start: r.SourceStart.Time.UTC(), End: r.SourceEnd.Time.UTC(), ObservedAt: r.ObservedAt.Time.UTC(), Attribution: a}}, nil
}

func (s *PgStore) AppendFinancialAdjustment(ctx context.Context, adjustment FinancialAdjustment) (FinancialUsageRecord, error) {
	account, err := parsePgUUID(adjustment.AccountID)
	if err != nil || !validFinancialAdjustment(adjustment) {
		return FinancialUsageRecord{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FinancialUsageRecord{}, fmt.Errorf("financial adjustment begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.FinancialEvidenceAccountLock(ctx, tx, account); err != nil {
		return FinancialUsageRecord{}, err
	}
	original, err := q.FinancialEvidenceBySource(ctx, tx, sqlc.FinancialEvidenceBySourceParams{AccountID: account, SourceID: adjustment.CorrectsSourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return FinancialUsageRecord{}, ErrNotFound
	}
	if err != nil {
		return FinancialUsageRecord{}, err
	}
	if original.CorrectsSourceID.Valid {
		return FinancialUsageRecord{}, ErrInvalidArgument
	}
	err = q.FinancialAdjustmentInsert(ctx, tx, sqlc.FinancialAdjustmentInsertParams{AccountID: account, SourceID: adjustment.SourceID, CorrectsSourceID: adjustment.CorrectsSourceID, Quantity: adjustment.Quantity, Actor: adjustment.Actor, Reason: adjustment.Reason})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return FinancialUsageRecord{}, ErrConflict
			case "23514":
				return FinancialUsageRecord{}, ErrInvalidArgument
			case "23503":
				return FinancialUsageRecord{}, ErrNotFound
			}
		}
		return FinancialUsageRecord{}, fmt.Errorf("financial adjustment insert: %w", err)
	}
	row, err := q.FinancialEvidenceBySource(ctx, tx, sqlc.FinancialEvidenceBySourceParams{AccountID: account, SourceID: adjustment.SourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return FinancialUsageRecord{}, ErrNotFound
	}
	if err != nil {
		return FinancialUsageRecord{}, err
	}
	if row.CorrectsSourceID.String != adjustment.CorrectsSourceID || row.Quantity != adjustment.Quantity || row.AdjustmentActor.String != adjustment.Actor || row.AdjustmentReason.String != adjustment.Reason {
		return FinancialUsageRecord{}, ErrConflict
	}
	out, err := financialEvidenceFromRow(row)
	if err != nil {
		return FinancialUsageRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FinancialUsageRecord{}, fmt.Errorf("financial adjustment commit: %w", err)
	}
	return out, nil
}

func (s *PgStore) PutFinancialPriceSnapshot(ctx context.Context, p FinancialPriceSnapshot) (FinancialPriceSnapshot, error) {
	if !validFinancialPrice(p) {
		return FinancialPriceSnapshot{}, ErrInvalidArgument
	}
	id, err := parsePgUUID(p.AccountID)
	if err != nil {
		return FinancialPriceSnapshot{}, ErrInvalidArgument
	}
	data, err := json.Marshal(p.Price)
	if err != nil {
		return FinancialPriceSnapshot{}, err
	}
	at, err := sqlc.New().FinancialPriceSnapshotPut(ctx, s.pool, sqlc.FinancialPriceSnapshotPutParams{AccountID: id, PeriodStart: financialTimestamp(p.PeriodStart), PeriodEnd: financialTimestamp(p.PeriodEnd), Meter: p.Price.Meter, Version: p.Price.Version, Price: data, Plan: string(p.Plan), EffectiveFrom: financialTimestamp(p.EffectiveFrom), DeliveryMode: p.DeliveryMode})
	if errors.Is(err, pgx.ErrNoRows) {
		return FinancialPriceSnapshot{}, ErrConflict
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return FinancialPriceSnapshot{}, ErrNotFound
	}
	if err != nil {
		return FinancialPriceSnapshot{}, fmt.Errorf("financial price snapshot: %w", err)
	}
	p.PeriodStart = p.PeriodStart.UTC()
	p.PeriodEnd = p.PeriodEnd.UTC()
	p.EffectiveFrom = p.EffectiveFrom.UTC()
	p.RecordedAt = at.Time.UTC()
	return p, nil
}

func (s *PgStore) ListFinancialPriceSnapshots(ctx context.Context, account string, start time.Time) ([]FinancialPriceSnapshot, error) {
	if account == "" || start.IsZero() {
		return nil, ErrInvalidArgument
	}
	id, err := parsePgUUID(account)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().FinancialPriceSnapshotsList(ctx, s.pool, sqlc.FinancialPriceSnapshotsListParams{AccountID: id, PeriodStart: financialTimestamp(start)})
	if err != nil {
		return nil, fmt.Errorf("financial price snapshots list: %w", err)
	}
	out := make([]FinancialPriceSnapshot, 0, len(rows))
	for _, r := range rows {
		p := FinancialPriceSnapshot{AccountID: pgUUIDString(r.AccountID), PeriodStart: r.PeriodStart.Time.UTC(), PeriodEnd: r.PeriodEnd.Time.UTC(), RecordedAt: r.RecordedAt.Time.UTC(), Plan: api.Plan(r.Plan), EffectiveFrom: r.EffectiveFrom.Time.UTC(), DeliveryMode: r.DeliveryMode}
		if err := json.Unmarshal(r.Price, &p.Price); err != nil {
			return nil, fmt.Errorf("financial price snapshot decode: %w", err)
		}
		if !validFinancialPrice(p) || p.Price.Meter != r.Meter || p.Price.Version != r.Version {
			return nil, fmt.Errorf("financial price snapshot: %w", ErrInvalidArgument)
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *PgStore) RecordFinancialSamplingWindow(ctx context.Context, minute time.Time, compute, egress bool) error {
	if minute.IsZero() || !minute.Equal(minute.Truncate(time.Minute)) {
		return ErrInvalidArgument
	}
	err := sqlc.New().FinancialSamplingWindowPut(ctx, s.pool, sqlc.FinancialSamplingWindowPutParams{Minute: financialTimestamp(minute), ComputeComplete: compute, EgressComplete: egress})
	if err != nil {
		return fmt.Errorf("financial sampling window: %w", err)
	}
	return nil
}

func (s *PgStore) FinancialSamplingCoverage(ctx context.Context, start, end time.Time) (FinancialSamplingCoverage, error) {
	if !validFinancialSamplingWindow(start, end) {
		return FinancialSamplingCoverage{}, ErrInvalidArgument
	}
	r, err := sqlc.New().FinancialSamplingCoverage(ctx, s.pool, sqlc.FinancialSamplingCoverageParams{PeriodStart: financialTimestamp(start), PeriodEnd: financialTimestamp(end)})
	if err != nil {
		return FinancialSamplingCoverage{}, fmt.Errorf("financial sampling coverage: %w", err)
	}
	out := FinancialSamplingCoverage{ComputeMinutes: r.ComputeMinutes, EgressMinutes: r.EgressMinutes}
	if r.ObservedAt.Valid {
		out.ObservedAt = r.ObservedAt.Time.UTC()
	}
	return out, nil
}

func (s *PgStore) AggregateFinancialUsage(ctx context.Context, account string, start, end time.Time, through int64) ([]FinancialUsageAggregate, error) {
	if account == "" || through < 0 || !validFinancialSamplingWindow(start, end) {
		return nil, ErrInvalidArgument
	}
	id, err := parsePgUUID(account)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().FinancialUsageAggregate(ctx, s.pool, sqlc.FinancialUsageAggregateParams{AccountID: id, PeriodStart: financialTimestamp(start), PeriodEnd: financialTimestamp(end), ThroughID: through, AllocationLimit: api.FinancialAllocationMax + 1})
	if err != nil {
		return nil, fmt.Errorf("financial usage aggregation: %w", err)
	}
	if len(rows) > api.FinancialAllocationMax {
		return nil, ErrFinancialAllocationLimit
	}
	out := make([]FinancialUsageAggregate, 0, len(rows))
	for _, row := range rows {
		var attribution financial.Attribution
		if err := json.Unmarshal(row.Attribution, &attribution); err != nil {
			return nil, fmt.Errorf("financial aggregate attribution: %w", err)
		}
		out = append(out, FinancialUsageAggregate{PriceVersion: row.PriceVersion.String, Plan: api.Plan(row.Plan), Meter: row.Meter, Unit: row.Unit, Attribution: attribution, Quantity: row.Quantity, SourceCount: row.SourceCount})
	}
	return out, nil
}
