package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type ProductWorkHistoryScope struct{ ProductPlanScope }

type ProductWorkHistoryStore interface {
	ListProductWorkHistory(context.Context, ProductWorkHistoryScope, api.ProductWorkHistoryFilter, string) (api.ProductWorkHistoryPage, error)
}

func workHistoryCursor(s ProductWorkHistoryScope, p api.ProductPermissions, f api.ProductWorkHistoryFilter, raw string) (productcontrol.WorkHistoryCursor, error) {
	if !p.ViewWork {
		return productcontrol.WorkHistoryCursor{}, ErrProductAccess
	}
	if normalized, err := productcontrol.NormalizeWorkHistoryFilter(f); err != nil || normalized != f {
		return productcontrol.WorkHistoryCursor{}, ErrInvalidArgument
	}
	scope := productcontrol.WorkHistoryScope(s.AccountID, s.AppID, s.TenantID, s.AuthAppID, s.OrganizationID, s.Role, p, f)
	c, err := productcontrol.DecodeWorkHistoryCursor(raw, scope)
	if err != nil {
		return c, ErrInvalidArgument
	}
	return c, nil
}

func workHistoryPage(s ProductWorkHistoryScope, p api.ProductPermissions, now time.Time, f api.ProductWorkHistoryFilter, c productcontrol.WorkHistoryCursor, entries []api.ProductWorkHistoryEntry) (api.ProductWorkHistoryPage, error) {
	page := api.ProductWorkHistoryPage{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, Filter: f, ObservedAt: now, HistoryScope: "retained_product_control_receipts", Entries: entries}
	if page.Entries == nil {
		page.Entries = []api.ProductWorkHistoryEntry{}
	}
	if len(page.Entries) > api.ProductControlWorkHistoryPageSize {
		page.Entries = page.Entries[:api.ProductControlWorkHistoryPageSize]
		page.NextCursor = productcontrol.EncodeWorkHistoryCursor(c.Scope, page.Entries[len(page.Entries)-1])
	}
	raw := ""
	if c.ID != "" {
		raw = productcontrol.EncodeWorkHistoryCursor(c.Scope, api.ProductWorkHistoryEntry{OperationID: c.ID, AdmittedAt: c.At})
	}
	if productcontrol.ValidateWorkHistoryPage(page, p, c.Scope, raw) != nil {
		return api.ProductWorkHistoryPage{}, ErrConflict
	}
	return page, nil
}

func collectWorkHistory(entries []api.ProductWorkHistoryEntry, e api.ProductWorkHistoryEntry, f api.ProductWorkHistoryFilter, c productcontrol.WorkHistoryCursor) []api.ProductWorkHistoryEntry {
	if !productcontrol.WorkHistoryMatches(e, f) || c.ID != "" && !productcontrol.WorkHistoryBefore(api.ProductWorkHistoryEntry{OperationID: c.ID, AdmittedAt: c.At}, e) {
		return entries
	}
	i := sort.Search(len(entries), func(i int) bool { return productcontrol.WorkHistoryBefore(e, entries[i]) })
	if i > api.ProductControlWorkHistoryPageSize {
		return entries
	}
	entries = append(entries, api.ProductWorkHistoryEntry{})
	copy(entries[i+1:], entries[i:])
	entries[i] = e
	if len(entries) > api.ProductControlWorkHistoryPageSize+1 {
		entries = entries[:api.ProductControlWorkHistoryPageSize+1]
	}
	return entries
}
