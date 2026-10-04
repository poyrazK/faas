package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RegionalRuntimeQuotaStore = (*PgStore)(nil)

func regionalQuotaError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		if p.ConstraintName == "regional_runtime_quota_order" || p.Code == "23505" {
			err = ErrRegionalRuntimeQuotaOrder
		} else if p.Code == "23514" {
			err = ErrConflict
		}
	}
	return fmt.Errorf("state: regional runtime quota: %w", err)
}

func regionalQuotaRow(row sqlc.RegionalRuntimeQuotaReceipt) (RegionalRuntimeQuotaReceipt, error) {
	r := RegionalRuntimeQuotaReceipt{CreatedAt: row.CreatedAt.Time}
	fields := []struct {
		raw   []byte
		value any
	}{
		{row.GrantClaims, &r.Grant}, {row.GrantEnvelope, &r.GrantEnvelope},
		{row.Retirement, &r.Retirement}, {row.RetirementEnvelope, &r.RetirementEnvelope},
	}
	for _, field := range fields {
		if len(field.raw) != 0 {
			if err := json.Unmarshal(field.raw, field.value); err != nil {
				return RegionalRuntimeQuotaReceipt{}, regionalQuotaError(err)
			}
		}
	}
	return r, nil
}

func readRegionalQuota(ctx context.Context, db sqlc.DBTX, appID, allocationID string) (RegionalRuntimeQuotaReceipt, error) {
	row, err := (&sqlc.Queries{}).RegionalRuntimeQuotaReceipt(ctx, db, sqlc.RegionalRuntimeQuotaReceiptParams{AppID: uuidToPgtype(appID), AllocationID: uuidToPgtype(allocationID)})
	if err != nil {
		return RegionalRuntimeQuotaReceipt{}, regionalQuotaError(err)
	}
	return regionalQuotaRow(row)
}

func readRegionalQuotaHead(ctx context.Context, db sqlc.DBTX, appID string) (RegionalRuntimeQuotaHead, error) {
	row, err := (&sqlc.Queries{}).RegionalRuntimeQuotaHead(ctx, db, uuidToPgtype(appID))
	if err != nil {
		return RegionalRuntimeQuotaHead{}, regionalQuotaError(err)
	}
	h := RegionalRuntimeQuotaHead{AdmissionEnabled: row.AdmissionEnabled}
	err = json.Unmarshal(row.GrantClaims, &h.Grant)
	return h, regionalQuotaError(err)
}

func (s *PgStore) RegionalRuntimeQuota(ctx context.Context, appID, allocationID string) (RegionalRuntimeQuotaReceipt, error) {
	if !validRegionalReleaseIDs(appID, allocationID, allocationID) {
		return RegionalRuntimeQuotaReceipt{}, ErrInvalidArgument
	}
	return readRegionalQuota(ctx, s.pool, appID, allocationID)
}

func (s *PgStore) RegionalRuntimeQuotaHead(ctx context.Context, appID string) (RegionalRuntimeQuotaHead, error) {
	if !validRegionalReleaseIDs(appID, appID, appID) {
		return RegionalRuntimeQuotaHead{}, ErrInvalidArgument
	}
	return readRegionalQuotaHead(ctx, s.pool, appID)
}

func regionalQuotaParams(r RegionalRuntimeQuotaReceipt) (sqlc.InsertRegionalRuntimeQuotaReceiptParams, error) {
	p := sqlc.InsertRegionalRuntimeQuotaReceiptParams{AppID: uuidToPgtype(r.Grant.Binding.RegionalAppID), AllocationID: uuidToPgtype(r.Grant.AllocationID), Generation: r.Grant.Generation}
	fields := []struct {
		value   any
		target  *[]byte
		present bool
	}{
		{r.Grant, &p.GrantClaims, true}, {r.GrantEnvelope, &p.GrantEnvelope, r.GrantEnvelope != nil},
		{r.Retirement, &p.Retirement, r.Retirement != nil}, {r.RetirementEnvelope, &p.RetirementEnvelope, r.RetirementEnvelope != nil},
	}
	for _, field := range fields {
		if field.present {
			raw, err := json.Marshal(field.value)
			if err != nil {
				return p, regionalQuotaError(err)
			}
			*field.target = raw
		}
	}
	return p, nil
}

func (s *PgStore) applyRegionalRuntimeQuota(ctx context.Context, m regionalRuntimeQuotaMutation) (RegionalRuntimeQuotaReceipt, error) {
	if !m.valid() {
		return RegionalRuntimeQuotaReceipt{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RegionalRuntimeQuotaReceipt{}, regionalQuotaError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	b := m.grant.Binding
	if err = q.LockRegionalRuntimeQuotaJournal(ctx, tx, uuidToPgtype(b.RegionalAppID)); err != nil {
		return RegionalRuntimeQuotaReceipt{}, regionalQuotaError(err)
	}
	old, err := readRegionalQuota(ctx, tx, b.RegionalAppID, m.grant.AllocationID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return RegionalRuntimeQuotaReceipt{}, err
	}
	exists := err == nil
	head, err := readRegionalQuotaHead(ctx, tx, b.RegionalAppID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return RegionalRuntimeQuotaReceipt{}, err
	}
	receipt, fresh, err := m.apply(old, exists, head, err == nil)
	if err != nil {
		return RegionalRuntimeQuotaReceipt{}, err
	}
	if fresh && m.retirement == nil {
		if _, err = q.LockRegionalRuntimeQuotaApp(ctx, tx, sqlc.LockRegionalRuntimeQuotaAppParams{AppID: uuidToPgtype(b.RegionalAppID), AccountID: uuidToPgtype(b.RegionalAccountID), OrgID: uuidToPgtype(b.RegionalOrgID), Slug: b.RegionalAppSlug}); err != nil {
			return RegionalRuntimeQuotaReceipt{}, regionalQuotaError(err)
		}
		if b.RegionalOrgID != "" {
			if _, err = q.LockRegionalRuntimeQuotaTenant(ctx, tx, uuidToPgtype(b.RegionalOrgID)); err != nil {
				return RegionalRuntimeQuotaReceipt{}, regionalQuotaError(err)
			}
		}
	}
	if fresh || (old.Retirement == nil && receipt.Retirement != nil) {
		params, encodeErr := regionalQuotaParams(receipt)
		if encodeErr != nil {
			return RegionalRuntimeQuotaReceipt{}, encodeErr
		}
		var row sqlc.RegionalRuntimeQuotaReceipt
		if fresh {
			row, err = q.InsertRegionalRuntimeQuotaReceipt(ctx, tx, params)
		} else {
			row, err = q.RetireRegionalRuntimeQuotaReceipt(ctx, tx, sqlc.RetireRegionalRuntimeQuotaReceiptParams{AppID: params.AppID, AllocationID: params.AllocationID, Retirement: params.Retirement, RetirementEnvelope: params.RetirementEnvelope})
		}
		if err != nil {
			return RegionalRuntimeQuotaReceipt{}, regionalQuotaError(err)
		}
		receipt, err = regionalQuotaRow(row)
		if err != nil {
			return RegionalRuntimeQuotaReceipt{}, err
		}
	}
	return receipt, regionalQuotaError(tx.Commit(ctx))
}
