package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ListInvoicesForAccount reads the entire invoice snapshot in one query,
// retaining the existing half-open UTC period-end filter and strict cursor.
func (s *PgStore) ListInvoicesForAccount(ctx context.Context, accountID string, month *time.Time, before time.Time, limit int) ([]Invoice, error) {
	id, err := parsePgUUID(accountID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 25
	}
	params := sqlc.ListInvoiceSnapshotsParams{AccountID: id, RowLimit: int32(min(limit, math.MaxInt32)), BeforeTime: pgtype.Timestamptz{Time: before.UTC(), Valid: !before.IsZero()}}
	if month != nil {
		start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
		params.MonthStart = pgtype.Timestamptz{Time: start, Valid: true}
		params.MonthEnd = pgtype.Timestamptz{Time: start.AddDate(0, 1, 0), Valid: true}
	}
	rows, err := sqlc.New().ListInvoiceSnapshots(ctx, s.pool, params)
	if err != nil {
		return nil, err
	}
	out := make([]Invoice, 0, len(rows))
	for _, row := range rows {
		inv, err := invoiceFromSnapshot(sqlc.GetInvoiceSnapshotRow(row))
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, nil
}

func (s *PgStore) GetInvoiceByID(ctx context.Context, id string) (Invoice, error) {
	u, err := parsePgUUID(id)
	if err != nil {
		return Invoice{}, err
	}
	row, err := sqlc.New().GetInvoiceSnapshot(ctx, s.pool, u)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invoice{}, ErrNotFound
	}
	if err != nil {
		return Invoice{}, err
	}
	return invoiceFromSnapshot(row)
}

func invoiceFromSnapshot(row sqlc.GetInvoiceSnapshotRow) (Invoice, error) {
	inv := Invoice{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID),
		Provider: row.Provider, ProviderInvoiceID: row.ProviderInvoiceID, ProviderChargeID: row.ProviderChargeID,
		Number: row.Number, Status: row.Status, PeriodStart: row.PeriodStart.Time, PeriodEnd: row.PeriodEnd.Time,
		SubtotalCents: row.SubtotalCents, TaxCents: row.TaxCents, TotalCents: row.TotalCents, AmountPaidCents: row.AmountPaidCents,
		Plan: api.Plan(row.Plan), AmountRefundedCents: row.AmountRefundedCents,
		AmountRefundPendingCents: row.AmountRefundPendingCents, CreditsAppliedCents: row.CreditsAppliedCents,
		Currency: row.Currency, PDFAvailable: row.PdfAvailable, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if string(row.Details) != "{}" {
		inv.Details = new(InvoiceDetails)
		if err := json.Unmarshal(row.Details, inv.Details); err != nil {
			return Invoice{}, err
		}
	}
	if string(row.DetailLifecycle) != "{}" {
		inv.Lifecycle = new(InvoiceLifecycle)
		if err := json.Unmarshal(row.DetailLifecycle, inv.Lifecycle); err != nil {
			return Invoice{}, fmt.Errorf("decode invoice lifecycle: %w", err)
		}
	}
	return inv, nil
}

// UpsertInvoice atomically merges supplied scalar facts and replaces a
// supplied line snapshot. Sparse deliveries preserve previously stored data.
// Refund, credit and historical-plan fields keep their previous semantics.
func (s *PgStore) UpsertInvoice(ctx context.Context, inv Invoice) error {
	if inv.Provider == "" || inv.ProviderInvoiceID == "" || inv.AccountID == "" {
		return errors.New("state: invoice account, provider, and provider_invoice_id are required")
	}
	details, err := encodeInvoiceDetails(inv.Details)
	if err != nil {
		return err
	}
	accountID, err := parsePgUUID(inv.AccountID)
	if err != nil {
		return err
	}
	if inv.PeriodStart.IsZero() {
		inv.PeriodStart = time.Now().UTC()
	}
	if inv.PeriodEnd.IsZero() {
		inv.PeriodEnd = inv.PeriodStart
	}
	if inv.Currency == "" {
		inv.Currency = "eur"
	}
	if inv.Status == "" {
		inv.Status = "open"
	}
	if !inv.Plan.Valid() {
		inv.Plan = api.PlanFree
	}
	initialLifecycle, err := json.Marshal(newInvoiceLifecycle(time.Now().UTC()))
	if err != nil {
		return err
	}
	params := sqlc.UpsertInvoiceSnapshotParams{
		AccountID: accountID, Provider: inv.Provider, ProviderInvoiceID: inv.ProviderInvoiceID, ProviderChargeID: inv.ProviderChargeID,
		Number: inv.Number, Status: inv.Status, PeriodStart: pgtype.Timestamptz{Time: inv.PeriodStart.UTC(), Valid: true},
		PeriodEnd: pgtype.Timestamptz{Time: inv.PeriodEnd.UTC(), Valid: true}, SubtotalCents: inv.SubtotalCents,
		TaxCents: inv.TaxCents, TotalCents: inv.TotalCents, AmountPaidCents: inv.AmountPaidCents,
		Plan: string(inv.Plan), Currency: strings.ToLower(inv.Currency), PdfAvailable: inv.PDFAvailable, Details: details,
		DetailLifecycle: initialLifecycle,
	}
	return s.upsertInvoiceWithLifecycle(ctx, params)
}

func (s *PgStore) upsertInvoiceWithLifecycle(ctx context.Context, params sqlc.UpsertInvoiceSnapshotParams) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin invoice upsert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.UpsertInvoiceSnapshot(ctx, tx, params)
	if err != nil {
		return fmt.Errorf("upsert invoice: %w", err)
	}
	inv, err := invoiceFromSnapshot(sqlc.GetInvoiceSnapshotRow(row))
	if err != nil {
		return err
	}
	lifecycle, err := advanceInvoiceLifecycle(inv, inv.UpdatedAt)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(lifecycle)
	if err != nil {
		return err
	}
	if err := q.SetInvoiceDetailLifecycle(ctx, tx, sqlc.SetInvoiceDetailLifecycleParams{ID: row.ID, DetailLifecycle: encoded}); err != nil {
		return fmt.Errorf("persist invoice lifecycle: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit invoice upsert: %w", err)
	}
	return nil
}
