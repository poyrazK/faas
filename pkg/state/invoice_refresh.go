package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func applyInvoiceRefresh(inv Invoice, expected time.Time, details *InvoiceDetails, now time.Time) (Invoice, error) {
	if expected.IsZero() || !inv.UpdatedAt.Equal(expected) {
		return Invoice{}, ErrConflict
	}
	if details == nil {
		return Invoice{}, errors.New("state: invoice refresh has no details")
	}
	now = now.UTC()
	// updated_at is the optimistic revision and PostgreSQL stores timestamps
	// at microsecond precision. Preserve a strictly increasing revision even
	// when the store clock has not advanced since the previous write.
	if !now.After(inv.UpdatedAt) {
		now = inv.UpdatedAt.Add(time.Microsecond)
	}
	inv.Details = mergeInvoiceDetails(inv.Details, stampInvoiceLines(inv.Details, details, now))
	if err := ValidateInvoiceDetails(inv.Details); err != nil {
		return Invoice{}, err
	}
	inv.UpdatedAt = now
	lifecycle, err := advanceInvoiceLifecycle(inv, now)
	if err != nil {
		return Invoice{}, err
	}
	inv.Lifecycle = lifecycle
	return inv, nil
}

func (m *MemStore) RefreshInvoiceDetails(ctx context.Context, accountID, id string, expected time.Time, details *InvoiceDetails) (Invoice, error) {
	if err := ValidateInvoiceDetails(details); err != nil {
		return Invoice{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Invoice{}, err
	}
	inv, ok := m.invoices[id]
	if !ok || inv.AccountID != accountID {
		return Invoice{}, ErrNotFound
	}
	updated, err := applyInvoiceRefresh(inv, expected, details, time.Now().UTC())
	if err != nil {
		return Invoice{}, err
	}
	m.invoices[id] = cloneInvoice(updated)
	return cloneInvoice(updated), nil
}

func (s *PgStore) RefreshInvoiceDetails(ctx context.Context, accountID, id string, expected time.Time, details *InvoiceDetails) (Invoice, error) {
	if err := ValidateInvoiceDetails(details); err != nil {
		return Invoice{}, err
	}
	u, err := parsePgUUID(id)
	if err != nil {
		return Invoice{}, ErrNotFound
	}
	owner, err := parsePgUUID(accountID)
	if err != nil {
		return Invoice{}, ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Invoice{}, fmt.Errorf("begin invoice refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockOwnedInvoiceSnapshot(ctx, tx, sqlc.LockOwnedInvoiceSnapshotParams{ID: u, AccountID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return Invoice{}, ErrNotFound
	}
	if err != nil {
		return Invoice{}, err
	}
	inv, err := invoiceFromSnapshot(sqlc.GetInvoiceSnapshotRow(row))
	if err != nil {
		return Invoice{}, err
	}
	// Use the database clock after acquiring the row lock, so queued refreshes
	// cannot assign a transaction-start timestamp older than a previous write.
	now, err := q.InvoiceRefreshTime(ctx, tx)
	if err != nil {
		return Invoice{}, err
	}
	updated, err := applyInvoiceRefresh(inv, expected, details, now.Time)
	if err != nil {
		return Invoice{}, err
	}
	encoded, err := json.Marshal(updated.Details)
	if err != nil {
		return Invoice{}, err
	}
	lifecycle, err := json.Marshal(updated.Lifecycle)
	if err != nil {
		return Invoice{}, err
	}
	if err := q.SetInvoiceEnrichment(ctx, tx, sqlc.SetInvoiceEnrichmentParams{ID: u, Details: encoded, DetailLifecycle: lifecycle, UpdatedAt: now}); err != nil {
		return Invoice{}, fmt.Errorf("persist invoice refresh: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Invoice{}, fmt.Errorf("commit invoice refresh: %w", err)
	}
	return updated, nil
}
