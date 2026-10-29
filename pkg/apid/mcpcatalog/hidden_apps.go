package mcpcatalog

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// hiddenApps names the account's company apps the request's principal may no
// longer see: those of a company the creator has left, or outside the key's
// app or organization (appaccess.HiddenApps). The account-wide connection
// health view names no app in its URL, so appaccess.Wrap never decides for
// it; it leaves these apps' rows out and refuses to act on them.
func hiddenApps(r *http.Request, store state.Store, account state.Account) (map[string]bool, error) {
	apps, err := store.ListApps(r.Context(), account.ID)
	if err != nil {
		return nil, err
	}
	return appaccess.HiddenApps(r, store, requestActor(r, account), apps)
}

func requestActor(r *http.Request, account state.Account) state.Account {
	if principal, _, ok := authmw.AccountFromContext(r); ok {
		return principal
	}
	return account
}

// RetirementVisibility decides which grant retirement records the request's
// principal may see and act on. The ledger outlives its apps while the
// account's app list holds only live ones, so each record is decided from its
// app's own row, deleted or not (appaccess.VisibleAppsContext): a deleted
// company app's records stay hidden from a creator who has left the company.
// A record whose app row is gone stays with the account that owns the ledger.
type RetirementVisibility struct {
	ctx   context.Context
	store state.Store
	actor state.Account
	seen  map[string]bool
}

func NewRetirementVisibility(r *http.Request, store state.Store, account state.Account) *RetirementVisibility {
	return &RetirementVisibility{ctx: r.Context(), store: store, actor: requestActor(r, account), seen: map[string]bool{}}
}

// Visible reports whether the principal may see the records of appID.
func (v *RetirementVisibility) Visible(appID string) (bool, error) {
	if visible, ok := v.seen[appID]; ok {
		return visible, nil
	}
	visible := true
	app, err := v.store.AppByID(v.ctx, appID)
	switch {
	case errors.Is(err, state.ErrNotFound):
	case err != nil:
		return false, err
	default:
		apps, err := appaccess.VisibleAppsContext(v.ctx, v.store, v.actor, []state.App{app})
		if err != nil {
			return false, err
		}
		visible = len(apps) == 1
	}
	v.seen[appID] = visible
	return visible, nil
}

// Filter drops the records the principal may not see from a ledger page. The
// page's next offset still follows the rows that were read, so hidden records
// never end paging.
func (v *RetirementVisibility) Filter(page api.MCPOAuthRetirementsResponse) (api.MCPOAuthRetirementsResponse, error) {
	items := make([]api.MCPOAuthRetirement, 0, len(page.Items))
	for _, record := range page.Items {
		visible, err := v.Visible(record.AppID)
		if err != nil {
			return api.MCPOAuthRetirementsResponse{}, err
		}
		if visible {
			items = append(items, record)
		}
	}
	page.Items = items
	return page, nil
}
