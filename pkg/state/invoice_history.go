package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func ValidateInvoiceHistoryInvoice(inv Invoice, accountID, provider string) error {
	if provider != "stripe" && provider != "paddle" && provider != "polar" {
		return errors.New("state: invalid provider for invoice history")
	}
	if inv.AccountID != accountID || inv.Provider != provider || inv.ProviderInvoiceID == "" ||
		len(inv.ProviderInvoiceID) > api.MaxFOCUSExportFieldBytes || strings.ContainsAny(inv.ProviderInvoiceID, "\r\n") ||
		!validInvoiceText(inv.Number, api.MaxInvoiceDetailTextBytes) || strings.ContainsAny(inv.Number, "\r\n") ||
		!validInvoiceText(inv.ProviderChargeID, api.MaxFOCUSExportFieldBytes) || strings.ContainsAny(inv.ProviderChargeID, "\r\n") ||
		inv.Plan != InvoicePlanUnknown || inv.Currency != "eur" || inv.PeriodStart.IsZero() || inv.PeriodEnd.IsZero() ||
		inv.PeriodEnd.Before(inv.PeriodStart) || inv.SubtotalCents < 0 || inv.TaxCents < 0 ||
		inv.TotalCents < 0 || inv.TaxCents > inv.TotalCents || inv.AmountPaidCents < 0 || inv.AmountPaidCents > inv.TotalCents {
		return errors.New("state: invalid provider invoice history record")
	}
	switch inv.Status {
	case "draft", "open", "paid", "uncollectible", "void":
	default:
		return errors.New("state: invalid provider invoice history status")
	}
	if inv.Lifecycle != nil {
		return errors.New("state: invoice history cannot supply lifecycle history")
	}
	return ValidateInvoiceDetails(inv.Details)
}

// ImportInvoiceHistory inserts the page atomically and never updates an
// existing webhook or invoice row when the natural key collides.
func (s *PgStore) ImportInvoiceHistory(ctx context.Context, accountID, provider string, invoices []Invoice) (int, error) {
	if len(invoices) > api.MaxInvoiceHistoryPageSize {
		return 0, errors.New("state: invoice history page exceeds limit")
	}
	for _, inv := range invoices {
		if err := ValidateInvoiceHistoryInvoice(inv, accountID, provider); err != nil {
			return 0, err
		}
	}
	if len(invoices) == 0 {
		return 0, nil
	}
	accountUUID, err := parsePgUUID(accountID)
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin invoice history import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	inserted := 0
	for _, inv := range invoices {
		now := time.Now().UTC()
		details, err := encodeInvoiceDetails(stampInvoiceLines(nil, inv.Details, now))
		if err != nil {
			return 0, err
		}
		lifecycle, err := json.Marshal(newInvoiceLifecycle(now))
		if err != nil {
			return 0, err
		}
		row, err := q.InsertInvoiceHistorySnapshot(ctx, tx, sqlc.InsertInvoiceHistorySnapshotParams{
			AccountID: accountUUID, Provider: provider, ProviderInvoiceID: inv.ProviderInvoiceID,
			ProviderChargeID: inv.ProviderChargeID, Number: inv.Number, Status: inv.Status,
			PeriodStart:   pgtype.Timestamptz{Time: inv.PeriodStart.UTC(), Valid: true},
			PeriodEnd:     pgtype.Timestamptz{Time: inv.PeriodEnd.UTC(), Valid: true},
			SubtotalCents: inv.SubtotalCents, TaxCents: inv.TaxCents, TotalCents: inv.TotalCents,
			AmountPaidCents: inv.AmountPaidCents, Plan: string(InvoicePlanUnknown), Currency: "eur",
			PdfAvailable: inv.PDFAvailable, Details: details, DetailLifecycle: lifecycle,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("insert provider invoice history: %w", err)
		}
		inserted++
		current := inv
		current.ID = pgUUIDString(row.ID)
		current.CreatedAt = row.UpdatedAt.Time
		current.UpdatedAt = row.UpdatedAt.Time
		current.Lifecycle = newInvoiceLifecycle(row.UpdatedAt.Time)
		current.Lifecycle, err = advanceInvoiceLifecycle(current, row.UpdatedAt.Time)
		if err != nil {
			return 0, err
		}
		encoded, err := json.Marshal(current.Lifecycle)
		if err != nil {
			return 0, err
		}
		if err := q.SetInvoiceDetailLifecycle(ctx, tx, sqlc.SetInvoiceDetailLifecycleParams{ID: row.ID, DetailLifecycle: encoded}); err != nil {
			return 0, fmt.Errorf("persist imported invoice lifecycle: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit invoice history import: %w", err)
	}
	return inserted, nil
}

func (m *MemStore) ImportInvoiceHistory(_ context.Context, accountID, provider string, invoices []Invoice) (int, error) {
	if len(invoices) > api.MaxInvoiceHistoryPageSize {
		return 0, errors.New("state: invoice history page exceeds limit")
	}
	for _, inv := range invoices {
		if err := ValidateInvoiceHistoryInvoice(inv, accountID, provider); err != nil {
			return 0, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inserted := 0
	seen := make(map[string]bool)
	for _, existing := range m.invoices {
		if existing.AccountID == accountID && existing.Provider == provider {
			seen[existing.ProviderInvoiceID] = true
		}
	}
	pending := make([]Invoice, 0, len(invoices))
	for _, inv := range invoices {
		if seen[inv.ProviderInvoiceID] {
			continue
		}
		seen[inv.ProviderInvoiceID] = true
		now := time.Now().UTC()
		inv.ID, inv.CreatedAt, inv.UpdatedAt = uuid.NewString(), now, now
		inv.Details = stampInvoiceLines(nil, inv.Details, now)
		inv.Lifecycle = newInvoiceLifecycle(now)
		var err error
		inv.Lifecycle, err = advanceInvoiceLifecycle(inv, now)
		if err != nil {
			return 0, err
		}
		pending = append(pending, cloneInvoice(inv))
		inserted++
	}
	for _, inv := range pending {
		m.invoices[inv.ID] = inv
	}
	return inserted, nil
}
