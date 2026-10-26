package mcpcatalog

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// hiddenApps names the account's company apps the request's principal may no
// longer see: those of a company the creator has left, or outside the key's
// app or organization (appaccess.HiddenApps). The account-wide connection
// health and retirement views name no app in their URL, so appaccess.Wrap
// never decides for them; they leave these apps' rows out and refuse to act
// on them.
func hiddenApps(r *http.Request, store state.Store, account state.Account) (map[string]bool, error) {
	apps, err := store.ListApps(r.Context(), account.ID)
	if err != nil {
		return nil, err
	}
	actor := account
	if principal, _, ok := authmw.AccountFromContext(r); ok {
		actor = principal
	}
	return appaccess.HiddenApps(r, store, actor, apps)
}

// VisibleRetirements drops the records of hidden apps from a ledger page.
// The page's next offset still follows the rows that were read, so hidden
// records never end paging.
func VisibleRetirements(page api.MCPOAuthRetirementsResponse, hidden map[string]bool) api.MCPOAuthRetirementsResponse {
	if len(hidden) == 0 {
		return page
	}
	items := make([]api.MCPOAuthRetirement, 0, len(page.Items))
	for _, record := range page.Items {
		if !hidden[record.AppID] {
			items = append(items, record)
		}
	}
	page.Items = items
	return page
}
