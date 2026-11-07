// adr: 20261103222927
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrAppResourceInventoryUnavailable = errors.New("state: app resource inventory unavailable")

// AppResourceInventoryRequest pins the app's storage namespace and tenant.
// Empty Scope never means all scopes: callers must select default explicitly.
type AppResourceInventoryRequest struct {
	AccountID, AppID, OrgID, Scope, Kind string
	Limit, Offset                        int
}

type AppResourceInventoryPage struct {
	Items   []api.AppResourceInventoryItem
	Total   int64
	HasMore bool
}

// Optional so adapters without metadata pagination fail closed rather than
// silently replacing a failed selected-app read with an account inventory.
type AppResourceInventoryReader interface {
	ListAppResourceInventory(context.Context, AppResourceInventoryRequest) (AppResourceInventoryPage, error)
}

func (q AppResourceInventoryRequest) Validate() error {
	if q.AccountID == "" || q.AppID == "" || api.ValidateScope(q.Scope) != nil || !api.ValidAppResourceKind(q.Kind) || q.Limit < 1 || q.Limit > api.AppResourceInventoryMaxPage || q.Offset < 0 || q.Offset > api.AppResourceInventoryMaxOffset {
		return ErrInvalidArgument
	}
	if (q.Kind == "domains" || q.Kind == "crons") && q.Scope != api.DefaultEnvScope {
		return ErrInvalidArgument
	}
	return nil
}

func DecodeAppResourceInventoryPage(raw []byte, total int64, q AppResourceInventoryRequest) (AppResourceInventoryPage, error) {
	page := AppResourceInventoryPage{Items: []api.AppResourceInventoryItem{}, Total: total}
	if err := json.Unmarshal(raw, &page.Items); err != nil {
		return page, fmt.Errorf("state: decode app resource inventory: %w", err)
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		page.HasMore = true
	}
	return page, nil
}

// Memory adapters page only the selected app/scope and use the same stable
// ordering as SQL. Domain and binding IDs break name ties.
func PageAppResourceInventory(items []api.AppResourceInventoryItem, q AppResourceInventoryRequest) AppResourceInventoryPage {
	sort.Slice(items, func(i, j int) bool {
		return appResourceInventoryKey(items[i], q.Kind) < appResourceInventoryKey(items[j], q.Kind)
	})
	start := min(q.Offset, len(items))
	end := min(start+q.Limit, len(items))
	return AppResourceInventoryPage{Items: items[start:end], Total: int64(len(items)), HasMore: end < len(items)}
}

func appResourceInventoryKey(v api.AppResourceInventoryItem, kind string) string {
	switch kind {
	case "domains":
		return v.Domain
	case "buckets":
		return v.Name + "/" + v.ID
	case "storage_bindings":
		return v.Bucket + "/" + v.ID
	case "databases":
		if v.Binding != nil {
			return v.ID + "/" + v.Binding.ID
		}
	}
	return v.ID
}
