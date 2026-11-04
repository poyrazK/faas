// adr: 20261103222927
package managedpostgres

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppResourceDatabaseInventoryMemory(t *testing.T) {
	store := NewMemoryStore()
	account, app, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
	d := readyMemoryDatabase(t, store, account, uuid.NewString(), "primary", time.Now().UTC())
	for _, target := range []struct{ app, scope, key string }{{app, "default", "DATABASE_URL"}, {app, "default", "OTHER_URL"}, {app, "staging", "DATABASE_URL"}, {other, "default", "DATABASE_URL"}} {
		b := testBinding(account, d.ID, target.app, uuid.NewString(), time.Now().UTC())
		b.Scope = target.scope
		b.EnvironmentKey = target.key
		if _, _, err := store.ReserveBinding(t.Context(), b); err != nil {
			t.Fatal(err)
		}
	}
	for _, offset := range []int{0, 1, 9} {
		q := state.AppResourceInventoryRequest{AccountID: account, AppID: app, Scope: "default", Kind: "databases", Limit: 1, Offset: offset}
		page, err := store.ListAppResourceInventory(t.Context(), q)
		if err != nil || page.Total != 2 || page.HasMore != (offset == 0) {
			t.Fatalf("page: %+v %v", page, err)
		}
		for _, item := range page.Items {
			if item.ID != d.ID || item.AppID != app || item.Scope != "default" || item.Binding == nil || item.Binding.AppID != app {
				t.Fatal("foreign binding metadata")
			}
		}
		raw, _ := json.Marshal(page.Items)
		if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "provider-") {
			t.Fatal("provider data leaked")
		}
	}
}

func TestAppResourceDatabaseInventoryPostgres(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	core := state.NewPgStore(pool)
	app, err := core.CreateApp(ctx, state.App{AccountID: account, Slug: "metadata-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	other, err := core.CreateApp(ctx, state.App{AccountID: account, Slug: "other-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	d := postgresReadyDatabase(t, store, account, "primary", time.Now().UTC())
	for _, target := range []struct{ app, scope, key string }{{app.ID, "default", "DATABASE_URL"}, {app.ID, "default", "OTHER_URL"}, {app.ID, "staging", "DATABASE_URL"}, {other.ID, "default", "DATABASE_URL"}} {
		b := testBinding(account, d.ID, target.app, uuid.NewString(), time.Now().UTC())
		b.Scope = target.scope
		b.EnvironmentKey = target.key
		if _, _, err := store.ReserveBinding(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	for _, offset := range []int{0, 1, 9} {
		page, err := store.ListAppResourceInventory(ctx, state.AppResourceInventoryRequest{AccountID: account, AppID: app.ID, OrgID: app.OrgID, Scope: "default", Kind: "databases", Limit: 1, Offset: offset})
		if err != nil || page.Total != 2 || page.HasMore != (offset == 0) {
			t.Fatalf("page: %+v %v", page, err)
		}
		for _, item := range page.Items {
			if item.ID != d.ID || item.Binding == nil || item.Binding.AppID != app.ID || item.Binding.Scope != "default" {
				t.Fatal("foreign database binding")
			}
		}
	}
}
