package managedpostgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingInventoryMem(t *testing.T) {
	store := NewMemoryStore()
	accountID := uuid.NewString()
	database := readyMemoryDatabase(t, store, accountID, uuid.NewString(), "primary", time.Now().UTC())
	bindingInventorySuite(t, store, store, accountID, database.ID, uuid.NewString(), uuid.NewString())
}

func TestBindingInventoryPG(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	database := postgresReadyDatabase(t, store, accountID, "primary", time.Now().UTC())
	stateStore := state.NewPgStore(pool)
	app, err := stateStore.CreateApp(ctx, state.App{AccountID: accountID, Slug: "inventory-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	other, err := stateStore.CreateApp(ctx, state.App{AccountID: accountID, Slug: "other-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	bindingInventorySuite(t, store, store, accountID, database.ID, app.ID, other.ID)
}

func bindingInventorySuite(t *testing.T, bindings BindingStore, inventory AppBindingInventoryStore, accountID, databaseID, appID, otherAppID string) {
	t.Helper()
	ctx := context.Background()
	for _, input := range []struct{ app, scope string }{{appID, "production"}, {appID, "staging"}, {otherAppID, "production"}} {
		binding := testBinding(accountID, databaseID, input.app, uuid.NewString(), time.Now().UTC())
		binding.Scope = input.scope
		if _, _, err := bindings.ReserveBinding(ctx, binding); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		account, app, scope string
		count               int
	}{
		{accountID, appID, "", 2}, {accountID, appID, "production", 1}, {accountID, appID, "missing", 0},
		{uuid.NewString(), appID, "", 0}, {accountID, otherAppID, "", 1},
	} {
		items, err := inventory.ListBindingsForApp(ctx, tc.account, tc.app, tc.scope)
		if err != nil || len(items) != tc.count {
			t.Fatalf("scope=%q count=%d items=%+v err=%v", tc.scope, tc.count, items, err)
		}
		for _, item := range items {
			if item.DatabaseName != "primary" || item.EnvironmentKey != "DATABASE_URL" || item.State != "provisioning" || item.CredentialGeneration != 1 || item.RotationPending {
				t.Fatalf("binding metadata=%+v", item)
			}
		}
	}
}
