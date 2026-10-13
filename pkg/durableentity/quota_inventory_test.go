package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestQuotaInventoryPaginatesAndFiltersWithoutMutation(t *testing.T) {
	f := newFixture(t)
	scope := f.id
	scope.AppID = "inventory-app"
	scope.EnvironmentID = "immutable-env"
	manager, err := Open(t.Context(), f.store, Options{RetainedBytesLimit: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 18; i++ {
		id := scope
		id.Key = fmt.Sprint(i)
		if i == 17 {
			id.TenantID = "other-tenant"
		}
		_, err := manager.Invoke(t.Context(), id, "inventory-owner", Request{ID: "seed", Payload: json.RawMessage(`{}`)}, func(context.Context, View) (Transition, error) {
			return Transition{Data: json.RawMessage(`{}`), Result: json.RawMessage(`{}`)}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	before := len(f.store.objects)
	seen := 0
	cursor := ""
	for {
		page, err := manager.ScanQuotaInventory(t.Context(), cursor, scope)
		if err != nil || len(page.Entities) > 16 {
			t.Fatal(page, err)
		}
		for _, e := range page.Entities {
			if e.RetainedBytes == nil || *e.RetainedBytes <= 0 || e.Entity.TenantID != scope.TenantID {
				t.Fatal(e)
			}
			seen++
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if seen != 17 || len(f.store.objects) != before {
		t.Fatal(seen, len(f.store.objects), before)
	}
	// A legacy head for the same app without immutable environment identity
	// cannot silently disappear from migration coverage.
	legacy := scope
	legacy.EnvironmentID = ""
	legacy.Key = "legacy"
	if _, err := manager.Acquire(t.Context(), legacy, "legacy-owner"); err != nil {
		t.Fatal(err)
	}
	cursor = ""
	for {
		page, err := manager.ScanQuotaInventory(t.Context(), cursor, scope)
		if errors.Is(err, ErrInventoryPending) {
			break
		}
		if err != nil || page.NextCursor == "" {
			t.Fatal("legacy identity was ignored", page, err)
		}
		cursor = page.NextCursor
	}
}
