package state

import (
	"context"
	"time"
)

// APIConsumerBillingRecord is the relevant statement and export for one
// customer. It omits usage lines and provider credentials.
type APIConsumerBillingRecord struct {
	StatementID         string
	StatementStatus     APIConsumerUsageStatementStatus
	StatementCreatedAt  time.Time
	UnpricedUnits       int64
	AmountDueMillicents int64
	InvoiceID           string
	Provider            string
	PaymentStatus       string
	Livemode            bool
	SentAt              *time.Time
	PaidAt              *time.Time
	AmountPaidMinor     *int64
}

type APIConsumerBillingProgressStore interface {
	GetAPIConsumerBillingRecord(ctx context.Context, accountID, appID, consumerID string) (APIConsumerBillingRecord, error)
}

func (r APIConsumerBillingRecord) LivePaid() bool {
	return r.Livemode && r.ConfirmedPayment()
}

func (r APIConsumerBillingRecord) ConfirmedPayment() bool {
	return r.InvoiceID != "" && r.PaymentStatus == InvoicePaymentPaid &&
		r.PaidAt != nil && r.AmountPaidMinor != nil && *r.AmountPaidMinor > 0
}

// Prefer the first confirmed live payment, then payable live invoices,
// then earlier stages. A later test or draft cannot undo a paid milestone.
func preferredAPIConsumerBillingRecord(a, b APIConsumerBillingRecord) APIConsumerBillingRecord {
	rankA, rankB := apiConsumerBillingRecordRank(a), apiConsumerBillingRecordRank(b)
	if rankA == rankB && a.LivePaid() && !a.PaidAt.Equal(*b.PaidAt) {
		if a.PaidAt.Before(*b.PaidAt) {
			return a
		}
		return b
	}
	if rankA > rankB || rankA == rankB && (a.StatementCreatedAt.After(b.StatementCreatedAt) ||
		a.StatementCreatedAt.Equal(b.StatementCreatedAt) && a.StatementID > b.StatementID) {
		return a
	}
	return b
}

func apiConsumerBillingRecordRank(r APIConsumerBillingRecord) int {
	switch {
	case r.LivePaid():
		return 7
	case r.InvoiceID != "" && r.Livemode && (r.PaymentStatus == InvoicePaymentOpen || r.PaymentStatus == InvoicePaymentPaid):
		return 6
	case r.InvoiceID != "" && r.Livemode && (r.PaymentStatus == InvoicePaymentDraft || r.PaymentStatus == ""):
		return 5
	case r.InvoiceID != "" && !r.Livemode && (r.PaymentStatus == InvoicePaymentPaid || r.PaymentStatus == InvoicePaymentOpen):
		return 4
	case r.InvoiceID != "" && !r.Livemode && (r.PaymentStatus == InvoicePaymentDraft || r.PaymentStatus == ""):
		return 3
	case r.InvoiceID == "" && r.UnpricedUnits == 0 && r.AmountDueMillicents > 0:
		return 2
	case r.StatementID != "":
		return 1
	default:
		return 0
	}
}

func (m *MemStore) GetAPIConsumerBillingRecord(_ context.Context, accountID, appID, consumerID string) (APIConsumerBillingRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	exports := make(map[string]StatementInvoiceExport)
	for _, e := range m.statementInvoiceExports {
		if e.AccountID == accountID && e.SourceKind == StatementSourceAppConsumer && e.Status == StatementInvoiceExportCompleted {
			exports[e.StatementID] = e
		}
	}
	var best APIConsumerBillingRecord
	for _, statement := range m.apiConsumerUsageStatements {
		if statement.AccountID != accountID || statement.AppID != appID || statement.ConsumerID != consumerID ||
			statement.Status == APIConsumerUsageStatementSuperseded {
			continue
		}
		e := exports[statement.ID]
		record := APIConsumerBillingRecord{StatementID: statement.ID, StatementStatus: statement.Status,
			StatementCreatedAt: statement.CreatedAt, UnpricedUnits: statement.UnpricedUnits, AmountDueMillicents: statement.AmountDueMillicents(),
			InvoiceID: e.ProviderInvoiceID, Provider: e.Provider, PaymentStatus: e.PaymentStatus, Livemode: e.Livemode,
			PaidAt: e.PaidAt, AmountPaidMinor: e.AmountPaidMinor}
		if e.StripeIssue != nil {
			record.SentAt = e.StripeIssue.SentAt
		}
		best = preferredAPIConsumerBillingRecord(record, best)
	}
	return best, nil
}

func (s *PgStore) GetAPIConsumerBillingRecord(ctx context.Context, accountID, appID, consumerID string) (APIConsumerBillingRecord, error) {
	rows, err := s.pool.Query(ctx, `select s.id, s.status, s.created_at, s.unpriced_units,
		       s.amount_millicents - s.credit_applied_millicents,
		       coalesce(e.provider_invoice_id, ''), coalesce(e.provider, ''),
		       coalesce(e.payment_status, ''), coalesce(e.livemode, false),
		       (e.stripe_issue->>'sent_at')::timestamptz, e.paid_at, e.amount_paid_minor
		  from api_consumer_usage_statements s
		  left join usage_statement_invoice_exports e
		    on e.account_id = s.account_id and e.statement_id = s.id
		   and e.source_kind = 'app_consumer' and e.status = 'completed'
		 where s.account_id = $1::uuid and s.app_id = $2::uuid and s.consumer_id = $3::uuid
		   and s.status in ('draft', 'finalized')`, accountID, appID, consumerID)
	if err != nil {
		return APIConsumerBillingRecord{}, err
	}
	defer rows.Close()
	var best APIConsumerBillingRecord
	for rows.Next() {
		var record APIConsumerBillingRecord
		var status string
		if err := rows.Scan(&record.StatementID, &status, &record.StatementCreatedAt, &record.UnpricedUnits, &record.AmountDueMillicents,
			&record.InvoiceID, &record.Provider, &record.PaymentStatus, &record.Livemode, &record.SentAt, &record.PaidAt, &record.AmountPaidMinor); err != nil {
			return APIConsumerBillingRecord{}, err
		}
		record.StatementStatus = APIConsumerUsageStatementStatus(status)
		best = preferredAPIConsumerBillingRecord(record, best)
	}
	return best, rows.Err()
}
