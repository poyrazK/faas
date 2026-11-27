package state

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ProductOwnerInvoiceQueueStore interface {
	ListProductOwnerInvoiceQueue(ctx context.Context, account, app, period, cursor string) (ProductOwnerInvoiceQueue, error)
}

type ProductOwnerInvoiceQueue struct {
	Items      []ProductOwnerInvoiceQueueItem
	NextCursor string
}

type ProductOwnerInvoiceQueueItem struct {
	DocumentID, TenantID, Name, Currency, Provider string
	AmountMillicents                               int64
}

func productOwnerInvoiceQueuePage(cursor productOwnerCursor, items []ProductOwnerInvoiceQueueItem) ProductOwnerInvoiceQueue {
	page := ProductOwnerInvoiceQueue{Items: items}
	if len(items) > productOwnerBillingQueuePageSize {
		page.Items = items[:productOwnerBillingQueuePageSize]
		cursor.ID = page.Items[len(page.Items)-1].DocumentID
		page.NextCursor = encodeProductOwnerCursor(cursor)
	}
	return page
}

func (s *PgStore) ListProductOwnerInvoiceQueue(ctx context.Context, account, app, period, raw string) (ProductOwnerInvoiceQueue, error) {
	cursor, err := productOwnerMonthCursor("invoice-queue", account, app, period, raw, time.Now())
	if err != nil {
		return ProductOwnerInvoiceQueue{}, err
	}
	if _, err := s.GetProductCatalog(ctx, account, app, ""); err != nil {
		return ProductOwnerInvoiceQueue{}, err
	}
	var after pgtype.UUID
	if cursor.ID != "" {
		after = mustPgUUID(cursor.ID)
	}
	rows, err := sqlc.New().ProductOwnerInvoiceQueue(ctx, s.pool, sqlc.ProductOwnerInvoiceQueueParams{
		AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Period: period,
		AfterDocument: after, PageLimit: productOwnerBillingQueuePageSize + 1,
	})
	if err != nil {
		return ProductOwnerInvoiceQueue{}, err
	}
	items := make([]ProductOwnerInvoiceQueueItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ProductOwnerInvoiceQueueItem{
			DocumentID: pgUUIDString(row.DocumentID), TenantID: pgUUIDString(row.TenantID), Name: row.Name,
			Currency: row.Currency, AmountMillicents: row.AmountMillicents, Provider: row.Provider,
		})
	}
	return productOwnerInvoiceQueuePage(cursor, items), nil
}

func (m *MemStore) ListProductOwnerInvoiceQueue(_ context.Context, account, app, period, raw string) (ProductOwnerInvoiceQueue, error) {
	cursor, err := productOwnerMonthCursor("invoice-queue", account, app, period, raw, time.Now())
	if err != nil {
		return ProductOwnerInvoiceQueue{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.productLocked()
	if a, ok := m.apps[app]; !ok || a.AccountID != account || a.Status == AppDeleted || d.heads[app] == "" {
		return ProductOwnerInvoiceQueue{}, ErrNotFound
	}
	exports := map[string]StatementInvoiceExport{}
	for _, e := range m.statementInvoiceExports {
		if e.AccountID == account && e.SourceKind == StatementSourceProductControl {
			exports[e.StatementID] = e
		}
	}
	var items []ProductOwnerInvoiceQueueItem
	for id, row := range d.documents {
		doc := row.document
		if row.account != account || row.app != app || doc.Period != period || id <= cursor.ID || doc.Kind != "statement" || doc.AmountMillicents <= 0 {
			continue
		}
		tenant, ok := m.platformTenants[doc.TenantID]
		if !ok || tenant.AccountID != account {
			continue
		}
		e := exports[id]
		handoff, recorded := d.handoffs[id]
		if e.Status == StatementInvoiceExportCompleted ||
			recorded && (e.Status != StatementInvoiceExportCreating || e.ProviderInvoiceID != handoff.ExternalInvoiceID) {
			continue
		}
		items = append(items, ProductOwnerInvoiceQueueItem{
			DocumentID: id, TenantID: doc.TenantID, Name: tenant.Name, Currency: doc.Currency,
			AmountMillicents: doc.AmountMillicents, Provider: e.Provider,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DocumentID < items[j].DocumentID })
	return productOwnerInvoiceQueuePage(cursor, items), nil
}
