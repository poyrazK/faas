// adr: 576

package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The MemStore allocator mirrors allocate_app_service_address_index once an
// account has used the whole range (TestMigrationAppServiceAddressIndexReuse
// pins the PostgreSQL side of the same scenario).
func TestMemStoreServiceAddressReuseAfterExhaustion(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "svc-addr@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	create := func(slug string) App {
		t.Helper()
		app, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: slug, Type: AppTypeApp, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1})
		if err != nil {
			t.Fatalf("CreateApp(%s): %v", slug, err)
		}
		return app
	}
	tombstone := func(app App, age time.Duration) {
		t.Helper()
		deletedAt := time.Now().Add(-age)
		m.mu.Lock()
		app = m.apps[app.ID]
		app.Status = AppDeleted
		app.DeletedAt = &deletedAt
		m.apps[app.ID] = app
		m.mu.Unlock()
	}
	index := func(id string) int {
		t.Helper()
		got, err := m.AppServiceAddressIndex(ctx, id)
		if err != nil {
			t.Fatalf("AppServiceAddressIndex(%s): %v", id, err)
		}
		return got
	}

	old, recent, live := create("old"), create("recent"), create("live")
	if index(old.ID) != 1 || index(recent.ID) != 2 || index(live.ID) != 3 {
		t.Fatalf("indices = %d,%d,%d; want 1,2,3", index(old.ID), index(recent.ID), index(live.ID))
	}
	tombstone(old, 25*time.Hour)
	tombstone(recent, time.Hour)
	if fresh := create("fresh"); index(fresh.ID) != 4 {
		t.Fatalf("pre-exhaustion index = %d, want 4 (tombstones keep theirs)", index(fresh.ID))
	}

	m.mu.Lock()
	m.serviceAddressCursors[acct.ID] = api.ServiceAddressIndexMax
	m.mu.Unlock()
	if reclaimer := create("reclaimer"); index(reclaimer.ID) != 1 {
		t.Fatalf("reclaimer index = %d, want the quarantined-out tombstone's 1", index(reclaimer.ID))
	}
	if got := index(old.ID); got != 0 {
		t.Fatalf("reclaimed tombstone kept index %d", got)
	}
	if got := index(recent.ID); got != 2 {
		t.Fatalf("tombstone inside the quarantine has index %d, want 2", got)
	}
	if next := create("next"); index(next.ID) != 5 {
		t.Fatalf("next index = %d, want 5", index(next.ID))
	}

	// Mirror the trigger's restore path directly: RestoreApp also enforces
	// the delete grace window, which these hand-made tombstones omit.
	m.mu.Lock()
	restored := m.apps[old.ID]
	restored.Status, restored.DeletedAt = AppActive, nil
	m.ensureServiceAddressIndexLocked(&restored)
	m.mu.Unlock()
	if got := index(restored.ID); got != 6 {
		t.Fatalf("restored tombstone without an index got %d, want 6", got)
	}
}
