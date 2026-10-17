package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Payment statuses of an exported provider invoice (ADR-1504). Paid, void and
// deleted are final; deleted means the provider no longer has the draft.
const (
	InvoicePaymentDraft         = "draft"
	InvoicePaymentOpen          = "open"
	InvoicePaymentPaid          = "paid"
	InvoicePaymentVoid          = "void"
	InvoicePaymentUncollectible = "uncollectible"
	InvoicePaymentDeleted       = "deleted"
)

// InvoicePaymentStatuses lists every payment status.
var InvoicePaymentStatuses = []string{InvoicePaymentDraft, InvoicePaymentOpen, InvoicePaymentPaid,
	InvoicePaymentVoid, InvoicePaymentUncollectible, InvoicePaymentDeleted}

// Statement invoice list filters beyond a single payment status.
const (
	InvoiceFilterUnpaid  = "unpaid"
	InvoiceFilterOverdue = "overdue"
)

// StatementInvoicePayment is a provider invoice's payment state.
type StatementInvoicePayment struct {
	Status               string
	AmountPaidMinor      int64
	AmountRemainingMinor int64
	DueAt                *time.Time
	PaidAt               *time.Time
	HostedInvoiceURL     string
}

// StatementInvoicePaymentStore follows exported invoices' payment status.
type StatementInvoicePaymentStore interface {
	// ClaimStatementInvoiceSyncs returns completed exports due for a status
	// sync, pushing each one's next sync to now+lease so replicas skip it.
	ClaimStatementInvoiceSyncs(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]StatementInvoiceExport, error)
	// RecordStatementInvoicePayment stores a synced status and returns the
	// previous one; a nil next stops syncing.
	RecordStatementInvoicePayment(ctx context.Context, exportID string, payment StatementInvoicePayment, syncedAt time.Time, next *time.Time) (string, error)
	// DeferStatementInvoiceSync retries a failed sync at next.
	DeferStatementInvoiceSync(ctx context.Context, exportID string, next time.Time) error
	// ListStatementInvoices pages an account's completed exports, newest
	// first, after the given cursor; filter is a payment status, unpaid,
	// overdue, or empty for all.
	ListStatementInvoices(ctx context.Context, accountID, filter string, now time.Time, after string, limit int) ([]StatementInvoiceExport, error)
	// ListOverdueStatementInvoices returns overdue invoices no digest has
	// reported, oldest due first.
	ListOverdueStatementInvoices(ctx context.Context, now time.Time, limit int) ([]StatementInvoiceExport, error)
	MarkStatementInvoicesOverdueNotified(ctx context.Context, exportIDs []string, at time.Time) error
}

var (
	_ StatementInvoicePaymentStore = (*MemStore)(nil)
	_ StatementInvoicePaymentStore = (*PgStore)(nil)
)

// ValidInvoiceFilter reports whether filter is accepted by
// ListStatementInvoices.
func ValidInvoiceFilter(filter string) bool {
	return filter == "" || filter == InvoiceFilterUnpaid || filter == InvoiceFilterOverdue || slices.Contains(InvoicePaymentStatuses, filter)
}

func validateInvoicePayment(p StatementInvoicePayment) error {
	if !slices.Contains(InvoicePaymentStatuses, p.Status) || p.AmountPaidMinor < 0 || p.AmountRemainingMinor < 0 ||
		len(p.HostedInvoiceURL) > 2048 || (p.HostedInvoiceURL != "" && !strings.HasPrefix(p.HostedInvoiceURL, "https://")) {
		return fmt.Errorf("%w: invalid invoice payment", ErrInvalidArgument)
	}
	return nil
}

// InvoiceCursor is the list cursor of an export: newest first by creation
// time, then ID.
func InvoiceCursor(e StatementInvoiceExport) string {
	return e.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + e.ID
}

func parseInvoiceCursor(cursor string) (time.Time, string, bool) {
	at, id, ok := strings.Cut(cursor, "|")
	if !ok {
		return time.Time{}, "", false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	return t, id, err == nil
}

func invoiceMatches(e StatementInvoiceExport, filter string, now time.Time) bool {
	switch filter {
	case "":
		return true
	case InvoiceFilterUnpaid:
		return e.PaymentStatus == InvoicePaymentDraft || e.PaymentStatus == InvoicePaymentOpen || e.PaymentStatus == InvoicePaymentUncollectible
	case InvoiceFilterOverdue:
		return e.Overdue(now)
	}
	return e.PaymentStatus == filter
}

// --- MemStore ---

func (m *MemStore) ClaimStatementInvoiceSyncs(_ context.Context, now time.Time, lease time.Duration, limit int) ([]StatementInvoiceExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []StatementInvoiceExport{}
	for _, e := range m.statementInvoiceExports {
		if e.Status == StatementInvoiceExportCompleted && e.NextStatusSyncAt != nil && !e.NextStatusSyncAt.After(now) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextStatusSyncAt.Before(*out[j].NextStatusSyncAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	next := now.Add(lease).UTC()
	for i := range out {
		stored := m.statementInvoiceExports[out[i].ID]
		stored.NextStatusSyncAt = &next
		m.statementInvoiceExports[out[i].ID] = stored
	}
	return out, nil
}

func (m *MemStore) RecordStatementInvoicePayment(_ context.Context, exportID string, p StatementInvoicePayment, syncedAt time.Time, next *time.Time) (string, error) {
	if err := validateInvoicePayment(p); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.statementInvoiceExports[exportID]
	if !ok {
		return "", ErrNotFound
	}
	prev := e.PaymentStatus
	paid, remaining, synced := p.AmountPaidMinor, p.AmountRemainingMinor, syncedAt.UTC()
	e.PaymentStatus, e.AmountPaidMinor, e.AmountRemainingMinor = p.Status, &paid, &remaining
	e.DueAt, e.PaidAt, e.HostedInvoiceURL, e.StatusSyncedAt, e.NextStatusSyncAt = p.DueAt, p.PaidAt, p.HostedInvoiceURL, &synced, next
	m.statementInvoiceExports[exportID] = e
	return prev, nil
}

func (m *MemStore) DeferStatementInvoiceSync(_ context.Context, exportID string, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.statementInvoiceExports[exportID]
	if !ok {
		return ErrNotFound
	}
	at := next.UTC()
	e.NextStatusSyncAt = &at
	m.statementInvoiceExports[exportID] = e
	return nil
}

func (m *MemStore) ListStatementInvoices(_ context.Context, accountID, filter string, now time.Time, after string, limit int) ([]StatementInvoiceExport, error) {
	if !ValidInvoiceFilter(filter) {
		return nil, fmt.Errorf("%w: unknown invoice filter", ErrInvalidArgument)
	}
	afterAt, afterID, paged := parseInvoiceCursor(after)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []StatementInvoiceExport{}
	for _, e := range m.statementInvoiceExports {
		if e.AccountID != accountID || e.Status != StatementInvoiceExportCompleted || !invoiceMatches(e, filter, now) {
			continue
		}
		if paged && (e.CreatedAt.After(afterAt) || (e.CreatedAt.Equal(afterAt) && e.ID >= afterID)) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ListOverdueStatementInvoices(_ context.Context, now time.Time, limit int) ([]StatementInvoiceExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []StatementInvoiceExport{}
	for _, e := range m.statementInvoiceExports {
		if e.Overdue(now) && e.OverdueNotifiedAt == nil {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueAt.Before(*out[j].DueAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) MarkStatementInvoicesOverdueNotified(_ context.Context, exportIDs []string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range exportIDs {
		if e, ok := m.statementInvoiceExports[id]; ok && e.OverdueNotifiedAt == nil {
			notified := at.UTC()
			e.OverdueNotifiedAt = &notified
			m.statementInvoiceExports[id] = e
		}
	}
	return nil
}

// --- PgStore ---

func (s *PgStore) queryStatementInvoiceExports(ctx context.Context, sql string, args ...any) ([]StatementInvoiceExport, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StatementInvoiceExport{}
	for rows.Next() {
		e, err := scanStatementInvoiceExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PgStore) ClaimStatementInvoiceSyncs(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]StatementInvoiceExport, error) {
	out, err := s.queryStatementInvoiceExports(ctx, `update usage_statement_invoice_exports
		   set next_status_sync_at = $2
		 where id in (select id from usage_statement_invoice_exports
		               where status = 'completed' and next_status_sync_at <= $1
		               order by next_status_sync_at limit $3 for update skip locked)
		returning `+statementInvoiceExportCols, now, now.Add(lease), limit)
	if err != nil {
		return nil, fmt.Errorf("claim statement invoice syncs: %w", err)
	}
	return out, nil
}

func (s *PgStore) RecordStatementInvoicePayment(ctx context.Context, exportID string, p StatementInvoicePayment, syncedAt time.Time, next *time.Time) (string, error) {
	if err := validateInvoicePayment(p); err != nil {
		return "", err
	}
	var prev string
	err := s.pool.QueryRow(ctx, `with prev as (
			select coalesce(payment_status, '') as status from usage_statement_invoice_exports where id = $1::uuid for update)
		update usage_statement_invoice_exports
		   set payment_status = $2, amount_paid_minor = $3, amount_remaining_minor = $4, due_at = $5, paid_at = $6,
		       hosted_invoice_url = nullif($7, ''), status_synced_at = $8, next_status_sync_at = $9
		  from prev
		 where id = $1::uuid
		returning prev.status`, exportID, p.Status, p.AmountPaidMinor, p.AmountRemainingMinor, p.DueAt, p.PaidAt,
		p.HostedInvoiceURL, syncedAt, next).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("record statement invoice payment: %w", err)
	}
	return prev, nil
}

func (s *PgStore) DeferStatementInvoiceSync(ctx context.Context, exportID string, next time.Time) error {
	tag, err := s.pool.Exec(ctx, `update usage_statement_invoice_exports set next_status_sync_at = $2 where id = $1::uuid`, exportID, next)
	if err != nil {
		return fmt.Errorf("defer statement invoice sync: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListStatementInvoices(ctx context.Context, accountID, filter string, now time.Time, after string, limit int) ([]StatementInvoiceExport, error) {
	if !ValidInvoiceFilter(filter) {
		return nil, fmt.Errorf("%w: unknown invoice filter", ErrInvalidArgument)
	}
	afterAt, afterID, paged := parseInvoiceCursor(after)
	if !paged {
		afterAt, afterID = now.Add(100*365*24*time.Hour), ""
	}
	out, err := s.queryStatementInvoiceExports(ctx, `select `+statementInvoiceExportCols+`
		  from usage_statement_invoice_exports
		 where account_id = $1::uuid and status = 'completed'
		   and case $2::text
		         when '' then true
		         when 'unpaid' then payment_status in ('draft', 'open', 'uncollectible')
		         when 'overdue' then payment_status = 'open' and due_at < $3 and coalesce(amount_remaining_minor, 1) > 0
		         else payment_status = $2::text
		       end
		   and (created_at < $4 or (created_at = $4 and id::text < $5::text))
		 order by created_at desc, id desc
		 limit $6`, accountID, filter, now, afterAt, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list statement invoices: %w", err)
	}
	return out, nil
}

func (s *PgStore) ListOverdueStatementInvoices(ctx context.Context, now time.Time, limit int) ([]StatementInvoiceExport, error) {
	out, err := s.queryStatementInvoiceExports(ctx, `select `+statementInvoiceExportCols+`
		  from usage_statement_invoice_exports
		 where payment_status = 'open' and overdue_notified_at is null and due_at < $1
		   and coalesce(amount_remaining_minor, 1) > 0
		 order by due_at limit $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list overdue statement invoices: %w", err)
	}
	return out, nil
}

func (s *PgStore) MarkStatementInvoicesOverdueNotified(ctx context.Context, exportIDs []string, at time.Time) error {
	if _, err := s.pool.Exec(ctx, `update usage_statement_invoice_exports set overdue_notified_at = $2
		 where id = any($1::uuid[]) and overdue_notified_at is null`, exportIDs, at); err != nil {
		return fmt.Errorf("mark overdue statement invoices notified: %w", err)
	}
	return nil
}
