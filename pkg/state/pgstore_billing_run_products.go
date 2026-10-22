package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Product Control customers of billing runs (ADR-20261018212918) live in
// billing_run_product_items, keyed by run, app and tenant.

func billingRunProductItemFromPG(row sqlc.BillingRunProductItemsRow) BillingRunItem {
	return BillingRunItem{RunID: pgUUIDString(row.RunID), SourceKind: StatementSourceProductControl, SourceID: pgUUIDString(row.TenantID),
		AppID: pgUUIDString(row.AppID), ExternalRef: row.ExternalRef, Outcome: row.Outcome, StatementID: pgUUIDString(row.DocumentID),
		Currency: row.Currency.String, AmountMillicents: row.AmountMillicents.Int64, InvoiceID: row.InvoiceID.String,
		ExportedCount: int(row.ExportedCount), Reason: row.Reason.String, Attempts: int(row.Attempts),
		NextAttemptAt: row.NextAttemptAt.Time, UpdatedAt: row.UpdatedAt.Time, PaymentStatus: row.PaymentStatus}
}

func discoverBillingRunProductItems(ctx context.Context, tx pgx.Tx, run BillingRun, now time.Time) error {
	q := sqlc.New()
	if err := q.BillingRunDiscoverProductItems(ctx, tx, sqlc.BillingRunDiscoverProductItemsParams{RunID: mustPgUUID(run.ID), Now: pgTime(now),
		AccountID: mustPgUUID(run.AccountID), Period: run.PeriodStart.UTC().Format("2006-01")}); err != nil {
		return fmt.Errorf("discover Product Control customers: %w", err)
	}
	start, end := productMonthBounds(run.PeriodStart.UTC().Format("2006-01"))
	if err := q.BillingRunDiscoverProductFeeItems(ctx, tx, sqlc.BillingRunDiscoverProductFeeItemsParams{RunID: mustPgUUID(run.ID), Now: pgTime(now),
		AccountID: mustPgUUID(run.AccountID), MonthStart: pgTime(start), MonthEnd: pgTime(end)}); err != nil {
		return fmt.Errorf("discover Product Control customers with plan fees: %w", err)
	}
	if err := q.BillingRunMarkProductDiscovered(ctx, tx, sqlc.BillingRunMarkProductDiscoveredParams{Now: pgTime(now), ID: mustPgUUID(run.ID)}); err != nil {
		return fmt.Errorf("mark Product Control customers discovered: %w", err)
	}
	return nil
}

func (s *PgStore) billingRunProductItems(ctx context.Context, runID, outcome, after string, limit int) ([]BillingRunItem, error) {
	rows, err := sqlc.New().BillingRunProductItems(ctx, s.pool, sqlc.BillingRunProductItemsParams{RunID: mustPgUUID(runID), Outcome: outcome,
		After: after, PageLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list billing run Product Control items: %w", err)
	}
	out := make([]BillingRunItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, billingRunProductItemFromPG(row))
	}
	return out, nil
}

func (s *PgStore) dueBillingRunProductItems(ctx context.Context, runID string, now time.Time, limit int) ([]BillingRunItem, error) {
	rows, err := sqlc.New().BillingRunDueProductItems(ctx, s.pool, sqlc.BillingRunDueProductItemsParams{RunID: mustPgUUID(runID), Now: pgTime(now),
		PageLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list due billing run Product Control items: %w", err)
	}
	out := make([]BillingRunItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, billingRunProductItemFromPG(sqlc.BillingRunProductItemsRow(row)))
	}
	return out, nil
}

func (s *PgStore) updateBillingRunProductItem(ctx context.Context, owner string, item BillingRunItem) error {
	text := func(v string) pgtype.Text { return pgtype.Text{String: v, Valid: v != ""} }
	n, err := sqlc.New().BillingRunUpdateProductItem(ctx, s.pool, sqlc.BillingRunUpdateProductItemParams{Outcome: item.Outcome,
		DocumentID: mustPgUUID(item.StatementID), Currency: text(item.Currency),
		AmountMillicents: pgtype.Int8{Int64: item.AmountMillicents, Valid: item.StatementID != ""}, InvoiceID: text(item.InvoiceID),
		ExportedCount: int32(item.ExportedCount), Reason: text(item.Reason), Attempts: int32(item.Attempts), NextAttemptAt: pgTime(item.NextAttemptAt),
		Owner: owner, RunID: mustPgUUID(item.RunID), AppID: mustPgUUID(item.AppID), TenantID: mustPgUUID(item.SourceID)})
	if err != nil {
		return fmt.Errorf("update billing run Product Control item: %w", err)
	}
	if n == 0 {
		return ErrBillingRunLeaseLost
	}
	return nil
}

func (s *PgStore) addBillingRunProductCounts(ctx context.Context, runs []BillingRun, index map[string]int) error {
	ids := make([]pgtype.UUID, len(runs))
	for i := range runs {
		ids[i] = mustPgUUID(runs[i].ID)
	}
	rows, err := sqlc.New().BillingRunProductCounts(ctx, s.pool, ids)
	if err != nil {
		return fmt.Errorf("count billing run Product Control items: %w", err)
	}
	for _, row := range rows {
		runs[index[pgUUIDString(row.RunID)]].Counts[row.Outcome] += int(row.N)
	}
	return nil
}
