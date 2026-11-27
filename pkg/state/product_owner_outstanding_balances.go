package state

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	ProductOwnerBalanceCurrent       = "current"
	ProductOwnerBalanceOverdue       = "overdue"
	ProductOwnerBalanceOver30        = "over_30"
	ProductOwnerBalanceNoDueDate     = "no_due_date"
	ProductOwnerBalanceUncollectible = "uncollectible"
)

type ProductOwnerOutstandingBalancesStore interface {
	ProductOwnerOutstandingBalances(ctx context.Context, account, app string, filter ProductOwnerBalanceFilter, now time.Time) (ProductOwnerOutstandingBalances, error)
}

type ProductOwnerBalanceFilter struct {
	Livemode                 bool
	Currency, Bucket, Cursor string
}

func (f ProductOwnerBalanceFilter) Valid() bool {
	if f.Bucket == "" {
		return f.Currency == "" && f.Cursor == ""
	}
	if !isUpperASCIICurrency(f.Currency) || len(f.Cursor) > api.ProductControlOwnerCursorMaxBytes {
		return false
	}
	switch f.Bucket {
	case ProductOwnerBalanceCurrent, ProductOwnerBalanceOverdue, ProductOwnerBalanceOver30,
		ProductOwnerBalanceNoDueDate, ProductOwnerBalanceUncollectible:
		return true
	}
	return false
}

type ProductOwnerOutstandingBalances struct {
	Totals     []ProductOwnerBalanceTotal
	Invoices   []ProductOwnerBalanceInvoice
	NextCursor string
}

type ProductOwnerBalanceTotal struct {
	Currency       string `json:"currency"`
	Bucket         string `json:"bucket"`
	RemainingMinor string `json:"remaining_minor"`
	InvoiceCount   int64  `json:"invoice_count"`
	RefreshCount   int64  `json:"refresh_count"`
	UnknownCount   int64  `json:"unknown_count"`
}

type ProductOwnerBalanceInvoice struct {
	Payment      ProductOwnerPaymentQueueItem
	Status       string
	NeedsRefresh bool
	sortDue      time.Time
}

func productOwnerBalanceCursor(account, app string, filter ProductOwnerBalanceFilter) (productOwnerCursor, error) {
	if !filter.Valid() {
		return productOwnerCursor{}, ErrInvalidArgument
	}
	scope := productOwnerCursorScope(fmt.Sprintf("outstanding-balances/%t", filter.Livemode), account, app, filter.Currency, filter.Bucket)
	return decodeProductOwnerCursor(filter.Cursor, scope, true)
}

func (b *ProductOwnerOutstandingBalances) page(cursor productOwnerCursor) {
	if len(b.Invoices) > productOwnerBillingQueuePageSize {
		b.Invoices = b.Invoices[:productOwnerBillingQueuePageSize]
		last := b.Invoices[len(b.Invoices)-1]
		cursor.At, cursor.ID = last.sortDue, last.Payment.DocumentID
		b.NextCursor = encodeProductOwnerCursor(cursor)
	}
}

// The aggregate sums remain decimal strings so bigint invoice amounts
// can be added without narrowing PostgreSQL's numeric sum.
func (s *PgStore) ProductOwnerOutstandingBalances(ctx context.Context, account, app string, filter ProductOwnerBalanceFilter, now time.Time) (ProductOwnerOutstandingBalances, error) {
	var result ProductOwnerOutstandingBalances
	cursor, err := productOwnerBalanceCursor(account, app, filter)
	if err != nil {
		return result, err
	}
	if _, err := s.GetProductCatalog(ctx, account, app, ""); err != nil {
		return result, err
	}
	params := sqlc.ProductOwnerOutstandingBalancesParams{
		AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Livemode: filter.Livemode, Currency: filter.Currency, Bucket: filter.Bucket,
		AsOf: pgTime(now), StaleBefore: pgTime(now.Add(-api.ConsumerOverdueStatusMaxAge)), Today: pgTime(now.UTC().Truncate(24 * time.Hour)),
		OlderThan: pgTime(now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -30)), PageLimit: productOwnerBillingQueuePageSize + 1,
	}
	if cursor.ID != "" {
		params.AfterID, params.AfterDue = mustPgUUID(cursor.ID), pgTime(cursor.At)
	}
	row, err := sqlc.New().ProductOwnerOutstandingBalances(ctx, s.pool, params)
	if err != nil {
		return result, fmt.Errorf("read outstanding balances: %w", err)
	}
	if err := json.Unmarshal(row.BalanceTotals, &result.Totals); err != nil {
		return result, fmt.Errorf("decode outstanding totals: %w", err)
	}
	if err := result.decodeInvoices(row.BalanceInvoices, filter.Livemode); err != nil {
		return result, fmt.Errorf("decode outstanding invoices: %w", err)
	}
	result.page(cursor)
	return result, nil
}

func (b *ProductOwnerOutstandingBalances) decodeInvoices(raw []byte, live bool) error {
	var snapshots []struct {
		DocumentID        string     `json:"document_id"`
		TenantID          string     `json:"tenant_id"`
		Period            string     `json:"period"`
		Name              string     `json:"name"`
		Currency          string     `json:"currency"`
		PaymentStatus     string     `json:"payment_status"`
		HostedInvoiceURL  string     `json:"hosted_invoice_url"`
		ProviderInvoiceID string     `json:"provider_invoice_id"`
		AmountRemaining   *int64     `json:"amount_remaining_minor"`
		DueAt             *time.Time `json:"due_at"`
		StatusSyncedAt    *time.Time `json:"status_synced_at"`
		SortDue           time.Time  `json:"sort_due"`
		NeedsRefresh      bool       `json:"needs_refresh"`
		StripeReminder    bool       `json:"stripe_reminder"`
		ReminderStartedAt time.Time  `json:"reminder_started_at"`
		ReminderSentAt    time.Time  `json:"reminder_sent_at"`
	}
	if err := json.Unmarshal(raw, &snapshots); err != nil {
		return err
	}
	for _, row := range snapshots {
		b.Invoices = append(b.Invoices, ProductOwnerBalanceInvoice{
			Status: row.PaymentStatus, NeedsRefresh: row.NeedsRefresh, sortDue: row.SortDue,
			Payment: ProductOwnerPaymentQueueItem{
				DocumentID: row.DocumentID, TenantID: row.TenantID, Period: row.Period, Name: row.Name, Currency: row.Currency,
				HostedInvoiceURL: row.HostedInvoiceURL, ProviderInvoiceID: row.ProviderInvoiceID, AmountRemainingMinor: row.AmountRemaining,
				DueAt: row.DueAt, StatusSyncedAt: row.StatusSyncedAt, Livemode: live, StripeReminder: row.StripeReminder,
				ReminderStartedAt: row.ReminderStartedAt, ReminderSentAt: row.ReminderSentAt,
			},
		})
	}
	return nil
}

func (m *MemStore) ProductOwnerOutstandingBalances(_ context.Context, account, app string, filter ProductOwnerBalanceFilter, now time.Time) (ProductOwnerOutstandingBalances, error) {
	var report ProductOwnerOutstandingBalances
	cursor, err := productOwnerBalanceCursor(account, app, filter)
	if err != nil {
		return report, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	d := m.productLocked()
	if a, ok := m.apps[app]; !ok || a.AccountID != account || a.Status == AppDeleted || d.heads[app] == "" {
		return report, ErrNotFound
	}
	type subtotal struct {
		ProductOwnerBalanceTotal
		balance big.Int
	}
	groups := map[[2]string]*subtotal{}
	today := now.UTC().Truncate(24 * time.Hour)
	staleBefore := now.Add(-api.ConsumerOverdueStatusMaxAge)
	for _, export := range m.statementInvoiceExports {
		if export.AccountID != account || export.SourceKind != StatementSourceProductControl ||
			export.Status != StatementInvoiceExportCompleted || export.Livemode != filter.Livemode || !productInvoiceUnpaid(export) {
			continue
		}
		record := d.documents[export.StatementID]
		doc := record.document
		tenant, ok := m.platformTenants[doc.TenantID]
		if record.account != account || record.app != app || doc.Kind != "statement" || !ok || tenant.AccountID != account {
			continue
		}
		bucket := productOwnerInvoiceBalanceBucket(export, today)
		needsRefresh := export.StatusSyncedAt == nil || export.StatusSyncedAt.Before(staleBefore) || export.AmountRemainingMinor == nil
		key := [2]string{export.Currency, bucket}
		group := groups[key]
		if group == nil {
			group = &subtotal{ProductOwnerBalanceTotal: ProductOwnerBalanceTotal{Currency: export.Currency, Bucket: bucket}}
			groups[key] = group
		}
		group.InvoiceCount++
		if needsRefresh {
			group.RefreshCount++
		}
		if export.AmountRemainingMinor == nil {
			group.UnknownCount++
		} else {
			group.balance.Add(&group.balance, big.NewInt(*export.AmountRemainingMinor))
		}
		if filter.Bucket != bucket || filter.Currency != export.Currency {
			continue
		}
		sortDue := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
		if export.DueAt != nil {
			sortDue = *export.DueAt
		}
		if cursor.ID != "" && (sortDue.Before(cursor.At) || (sortDue.Equal(cursor.At) && export.StatementID <= cursor.ID)) {
			continue
		}
		report.Invoices = append(report.Invoices, ProductOwnerBalanceInvoice{
			Status: export.PaymentStatus, NeedsRefresh: needsRefresh, sortDue: sortDue,
			Payment: memProductOwnerPaymentItem(export, doc, tenant.Name, now),
		})
	}

	for _, group := range groups {
		group.RemainingMinor = group.balance.String()
		report.Totals = append(report.Totals, group.ProductOwnerBalanceTotal)
	}
	sort.Slice(report.Totals, func(i, j int) bool {
		a, b := report.Totals[i], report.Totals[j]
		if a.Currency != b.Currency {
			return a.Currency < b.Currency
		}
		return a.Bucket < b.Bucket
	})
	sort.Slice(report.Invoices, func(i, j int) bool {
		a, b := report.Invoices[i], report.Invoices[j]
		if !a.sortDue.Equal(b.sortDue) {
			return a.sortDue.Before(b.sortDue)
		}
		return a.Payment.DocumentID < b.Payment.DocumentID
	})
	report.page(cursor)
	return report, nil
}

func productOwnerInvoiceBalanceBucket(export StatementInvoiceExport, today time.Time) string {
	switch {
	case export.PaymentStatus == InvoicePaymentUncollectible:
		return ProductOwnerBalanceUncollectible
	case export.DueAt == nil:
		return ProductOwnerBalanceNoDueDate
	case export.DueAt.Before(today.AddDate(0, 0, -30)):
		return ProductOwnerBalanceOver30
	case export.DueAt.Before(today):
		return ProductOwnerBalanceOverdue
	default:
		return ProductOwnerBalanceCurrent
	}
}
