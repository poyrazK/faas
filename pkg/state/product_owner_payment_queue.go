package state

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ProductOwnerPaymentQueueStore interface {
	ListProductOwnerPaymentQueue(ctx context.Context, account, app, cursor string, now time.Time) (ProductOwnerPaymentQueue, error)
}

type ProductOwnerPaymentQueue struct {
	Items      []ProductOwnerPaymentQueueItem
	NextCursor string
}

type ProductOwnerPaymentAction string

const (
	ProductOwnerPaymentDraft        ProductOwnerPaymentAction = "draft"
	ProductOwnerPaymentOverdue      ProductOwnerPaymentAction = "overdue"
	ProductOwnerPaymentStale        ProductOwnerPaymentAction = "stale"
	ProductOwnerPaymentStripeSend   ProductOwnerPaymentAction = "stripe_send"
	ProductOwnerPaymentStripeReview ProductOwnerPaymentAction = "stripe_review"
)

type ProductOwnerPaymentQueueItem struct {
	DocumentID, TenantID, Period, Name, Currency, HostedInvoiceURL string
	ProviderInvoiceID                                              string
	Livemode, PaddleDraft, StripeSend, StripeReminder              bool
	Action                                                         ProductOwnerPaymentAction
	AmountRemainingMinor                                           *int64
	DueAt, StatusSyncedAt                                          *time.Time
	ReminderStartedAt, ReminderSentAt                              time.Time
}

func productOwnerPaymentQueuePage(cursor productOwnerCursor, items []ProductOwnerPaymentQueueItem) ProductOwnerPaymentQueue {
	page := ProductOwnerPaymentQueue{Items: items}
	if len(items) > productOwnerBillingQueuePageSize {
		page.Items = items[:productOwnerBillingQueuePageSize]
		last := page.Items[len(page.Items)-1]
		cursor.Period, cursor.ID = last.Period, last.DocumentID
		page.NextCursor = encodeProductOwnerCursor(cursor)
	}
	return page
}

func (s *PgStore) ListProductOwnerPaymentQueue(ctx context.Context, account, app, raw string, now time.Time) (ProductOwnerPaymentQueue, error) {
	cursor, err := decodeProductOwnerCursor(raw, productOwnerCursorScope("payment-queue", account, app, "", ""), false)
	if err != nil {
		return ProductOwnerPaymentQueue{}, err
	}
	if _, err := s.GetProductCatalog(ctx, account, app, ""); err != nil {
		return ProductOwnerPaymentQueue{}, err
	}
	var after pgtype.UUID
	if cursor.ID != "" {
		after = mustPgUUID(cursor.ID)
	}
	rows, err := sqlc.New().ProductOwnerPaymentQueue(ctx, s.pool, sqlc.ProductOwnerPaymentQueueParams{
		AccountID: mustPgUUID(account), AppID: mustPgUUID(app), AsOf: pgTime(now),
		StaleBefore: pgTime(now.Add(-api.ConsumerOverdueStatusMaxAge)), StripeRetryBefore: pgTime(now.Add(-api.ProductStripeInvoiceSendRetryWindow)),
		AfterPeriod: cursor.Period, AfterDocument: after, PageLimit: productOwnerBillingQueuePageSize + 1,
	})
	if err != nil {
		return ProductOwnerPaymentQueue{}, err
	}
	items := make([]ProductOwnerPaymentQueueItem, 0, len(rows))
	for _, row := range rows {
		item := ProductOwnerPaymentQueueItem{
			DocumentID: pgUUIDString(row.DocumentID), TenantID: pgUUIDString(row.TenantID), Period: row.Period,
			ProviderInvoiceID: row.ProviderInvoiceID, Livemode: row.Livemode, StripeReminder: row.StripeReminder,
			ReminderStartedAt: row.ReminderStartedAt.Time, ReminderSentAt: row.ReminderSentAt.Time,
			Name: row.Name, Currency: row.Currency, HostedInvoiceURL: row.HostedInvoiceUrl, PaddleDraft: row.PaddleDraft, StripeSend: row.StripeSend, Action: ProductOwnerPaymentAction(row.Action),
		}
		if row.AmountRemainingMinor.Valid {
			item.AmountRemainingMinor = &row.AmountRemainingMinor.Int64
		}
		if row.DueAt.Valid {
			item.DueAt = &row.DueAt.Time
		}
		if row.StatusSyncedAt.Valid {
			item.StatusSyncedAt = &row.StatusSyncedAt.Time
		}
		items = append(items, item)
	}
	return productOwnerPaymentQueuePage(cursor, items), nil
}

func (m *MemStore) ListProductOwnerPaymentQueue(_ context.Context, account, app, raw string, now time.Time) (ProductOwnerPaymentQueue, error) {
	cursor, err := decodeProductOwnerCursor(raw, productOwnerCursorScope("payment-queue", account, app, "", ""), false)
	if err != nil {
		return ProductOwnerPaymentQueue{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	d := m.productLocked()
	if a, ok := m.apps[app]; !ok || a.AccountID != account || a.Status == AppDeleted || d.heads[app] == "" {
		return ProductOwnerPaymentQueue{}, ErrNotFound
	}
	var items []ProductOwnerPaymentQueueItem
	for _, export := range m.statementInvoiceExports {
		if export.AccountID != account || export.SourceKind != StatementSourceProductControl || export.Status != StatementInvoiceExportCompleted {
			continue
		}
		id := export.StatementID
		row := d.documents[id]
		doc := row.document
		if row.account != account || row.app != app || doc.Kind != "statement" ||
			doc.Period < cursor.Period || (doc.Period == cursor.Period && id <= cursor.ID) {
			continue
		}
		tenant, ok := m.platformTenants[doc.TenantID]
		if !ok || tenant.AccountID != account {
			continue
		}
		action := productOwnerPaymentAction(export, now)
		if action == "" {
			continue
		}
		item := memProductOwnerPaymentItem(export, doc, tenant.Name, now)
		item.Action = action
		item.PaddleDraft = export.Provider == BillingProviderPaddle && export.PaymentStatus == InvoicePaymentDraft
		item.StripeSend = export.Provider == BillingProviderStripe && export.Collection == CollectionNone &&
			(action == ProductOwnerPaymentStripeSend || action == ProductOwnerPaymentStripeReview || (export.PaymentStatus == InvoicePaymentDraft && export.stripeSend.Stage != StripeInvoiceSent))
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Period != items[j].Period {
			return items[i].Period < items[j].Period
		}
		return items[i].DocumentID < items[j].DocumentID
	})
	return productOwnerPaymentQueuePage(cursor, items), nil
}

func productOwnerPaymentAction(export StatementInvoiceExport, now time.Time) ProductOwnerPaymentAction {
	if export.PaymentStatus != "" && export.PaymentStatus != InvoicePaymentDraft && !productInvoiceUnpaid(export) {
		return ""
	}
	if export.stripeSend.Stage == StripeInvoiceSending && !now.Before(export.stripeSend.SendStartedAt.Add(api.ProductStripeInvoiceSendRetryWindow)) {
		return ProductOwnerPaymentStripeReview
	}
	if export.stripeSend.Stage != "" && export.stripeSend.Stage != StripeInvoiceSent {
		return ProductOwnerPaymentStripeSend
	}
	// Give new exports one freshness window for the first successful sync.
	last := export.CreatedAt
	if export.CompletedAt != nil {
		last = *export.CompletedAt
	}
	if export.StatusSyncedAt != nil {
		last = *export.StatusSyncedAt
	}
	switch {
	case last.Before(now.Add(-api.ConsumerOverdueStatusMaxAge)):
		return ProductOwnerPaymentStale
	case export.PaymentStatus == InvoicePaymentDraft && export.stripeSend.Stage != StripeInvoiceSent && (export.StatusSyncedAt != nil || export.Collection == CollectionNone):
		return ProductOwnerPaymentDraft
	case export.Overdue(now):
		return ProductOwnerPaymentOverdue
	default:
		return ""
	}
}

func memProductOwnerPaymentItem(export StatementInvoiceExport, doc api.ProductBillingDocument, name string, now time.Time) ProductOwnerPaymentQueueItem {
	return ProductOwnerPaymentQueueItem{
		DocumentID: doc.ID, TenantID: doc.TenantID, Period: doc.Period, Name: name, Currency: export.Currency,
		ProviderInvoiceID: export.ProviderInvoiceID, Livemode: export.Livemode,
		StripeReminder:    export.stripeSend.Stage == StripeInvoiceSent && export.Overdue(now) && export.AmountRemainingMinor != nil,
		ReminderStartedAt: export.stripeSend.Reminder.StartedAt, ReminderSentAt: export.stripeSend.Reminder.SentAt,
		AmountRemainingMinor: export.AmountRemainingMinor, DueAt: export.DueAt,
		StatusSyncedAt: export.StatusSyncedAt, HostedInvoiceURL: export.HostedInvoiceURL,
	}
}
