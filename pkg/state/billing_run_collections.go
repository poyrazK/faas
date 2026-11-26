package state

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type BillingRunCollectionsStore interface {
	BillingRunCollections(ctx context.Context, accountID, runID string, now time.Time) ([]api.BillingRunCollectionTotal, error)
}

type collectionCurrency struct {
	currency string
	livemode bool
}

type runCollectionTotals map[collectionCurrency]api.BillingRunCollectionTotal

func (totals runCollectionTotals) add(invoice StatementInvoiceExport, sent bool, now time.Time) error {
	status := invoice.PaymentStatus
	issued := status == InvoicePaymentOpen || status == InvoicePaymentUncollectible || status == InvoicePaymentPaid
	if !issued && (!sent || status == InvoicePaymentVoid || status == InvoicePaymentDeleted) {
		return nil
	}
	key := collectionCurrency{invoice.Currency, invoice.Livemode}
	total := totals[key]
	total.Currency, total.Livemode = invoice.Currency, invoice.Livemode
	if !issued || invoice.AmountPaidMinor == nil || invoice.AmountRemainingMinor == nil {
		total.AwaitingCheck++
	}
	paid, outstanding, overdue := int64(0), int64(0), int64(0)
	if issued && invoice.AmountPaidMinor != nil {
		paid = *invoice.AmountPaidMinor
	}
	if (status == InvoicePaymentOpen || status == InvoicePaymentUncollectible) && invoice.AmountRemainingMinor != nil {
		outstanding = *invoice.AmountRemainingMinor
		if invoice.Overdue(now) {
			overdue = outstanding
		}
	}
	if paid > math.MaxInt64-total.AmountPaidMinor || outstanding > math.MaxInt64-total.AmountOutstandingMinor {
		return fmt.Errorf("billing run collection totals exceed minor-unit limits for %s", invoice.Currency)
	}
	total.AmountPaidMinor += paid
	total.AmountOutstandingMinor += outstanding
	total.AmountOverdueMinor += overdue
	if (paid > 0 || outstanding > 0) && invoice.DueAt != nil && !invoice.DueAt.After(now) {
		total.InvoicesDue++
		if status == InvoicePaymentPaid && paid > 0 && invoice.PaidAt != nil && !invoice.PaidAt.After(*invoice.DueAt) {
			total.PaidByDueDate++
		}
	}
	totals[key] = total
	return nil
}

func (totals runCollectionTotals) sorted() []api.BillingRunCollectionTotal {
	out := make([]api.BillingRunCollectionTotal, 0, len(totals))
	for _, total := range totals {
		out = append(out, total)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Livemode != out[j].Livemode {
			return out[i].Livemode
		}
		return out[i].Currency < out[j].Currency
	})
	return out
}

func (m *MemStore) BillingRunCollections(_ context.Context, accountID, runID string, now time.Time) ([]api.BillingRunCollectionTotal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.billingRuns[runID]
	if !ok || run.AccountID != accountID {
		return nil, ErrNotFound
	}
	statements := make(map[string]bool)
	for _, item := range m.billingRunItems[runID] {
		if item.SourceKind == StatementSourceAppConsumer && item.StatementID != "" {
			statements[item.StatementID] = true
		}
	}
	totals := make(runCollectionTotals)
	for _, invoice := range m.statementInvoiceExports {
		if invoice.AccountID != accountID || invoice.SourceKind != StatementSourceAppConsumer || !statements[invoice.StatementID] ||
			invoice.Provider != BillingProviderStripe || invoice.Status != StatementInvoiceExportCompleted {
			continue
		}
		if err := totals.add(invoice, invoice.StripeIssue != nil && invoice.StripeIssue.SentAt != nil, now); err != nil {
			return nil, err
		}
	}
	return totals.sorted(), nil
}

func (s *PgStore) BillingRunCollections(ctx context.Context, accountID, runID string, now time.Time) ([]api.BillingRunCollectionTotal, error) {
	rows, err := s.pool.Query(ctx, `select e.currency, e.livemode, coalesce(e.payment_status, ''),
		       e.amount_paid_minor, e.amount_remaining_minor, e.due_at, e.paid_at, e.stripe_issue->>'sent_at' is not null
		  from billing_runs r
		  join billing_run_items i on i.run_id = r.id and i.source_kind = 'app_consumer'
		  join usage_statement_invoice_exports e
		    on e.account_id = r.account_id and e.source_kind = i.source_kind and e.statement_id = i.statement_id
		 where r.id = $1::uuid and r.account_id = $2::uuid and e.provider = 'stripe' and e.status = 'completed'`, runID, accountID)
	if err != nil {
		return nil, fmt.Errorf("read billing run collections: %w", err)
	}
	defer rows.Close()
	totals := make(runCollectionTotals)
	for rows.Next() {
		var invoice StatementInvoiceExport
		var sent bool
		if err := rows.Scan(&invoice.Currency, &invoice.Livemode, &invoice.PaymentStatus,
			&invoice.AmountPaidMinor, &invoice.AmountRemainingMinor, &invoice.DueAt, &invoice.PaidAt, &sent); err != nil {
			return nil, err
		}
		if err := totals.add(invoice, sent, now); err != nil {
			return nil, err
		}
	}
	return totals.sorted(), rows.Err()
}
