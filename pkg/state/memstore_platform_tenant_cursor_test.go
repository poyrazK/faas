package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemPlatformTenantPageCursorSurvivesNewTenantAndTiedTimestamps(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "platform-tenant-cursor@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}

	original := make(map[string]struct{}, 3)
	for i := 0; i < 3; i++ {
		tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, string(rune('a'+i)), "Customer", 10)
		if err != nil {
			t.Fatal(err)
		}
		original[tenant.ID] = struct{}{}
	}

	// Force a timestamp tie so the ID portion of the cursor determines order.
	tiedAt := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	store.mu.Lock()
	for id, tenant := range store.platformTenants {
		if tenant.AccountID == account.ID {
			tenant.CreatedAt = tiedAt
			store.platformTenants[id] = tenant
		}
	}
	store.mu.Unlock()

	first, token, err := store.ListPlatformTenantsPage(ctx, account.ID, 1, 0, "")
	if err != nil || len(first) != 1 || token == "" {
		t.Fatalf("first page = %+v, token %q, err %v", first, token, err)
	}

	inserted, _, err := store.CreatePlatformTenant(ctx, account.ID, "new", "New customer", 10)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	inserted.CreatedAt = tiedAt.Add(time.Second)
	store.platformTenants[inserted.ID] = inserted
	store.mu.Unlock()

	second, nextToken, err := store.ListPlatformTenantsPage(ctx, account.ID, 1, 0, token)
	if err != nil || len(second) != 1 {
		t.Fatalf("second page = %+v, token %q, err %v", second, nextToken, err)
	}
	if second[0].ID == first[0].ID || second[0].ID == inserted.ID {
		t.Fatalf("cursor page repeated or included post-page insert: first=%s second=%s inserted=%s", first[0].ID, second[0].ID, inserted.ID)
	}
	if _, ok := original[second[0].ID]; !ok {
		t.Fatalf("cursor returned tenant outside original snapshot: %s", second[0].ID)
	}
	if nextToken == "" {
		t.Fatal("expected a continuation token for the remaining original tenant")
	}
	last, endToken, err := store.ListPlatformTenantsPage(ctx, account.ID, 1, 0, nextToken)
	if err != nil || len(last) != 1 || endToken != "" {
		t.Fatalf("last page = %+v, token %q, err %v", last, endToken, err)
	}
	if _, ok := original[last[0].ID]; !ok || last[0].ID == first[0].ID || last[0].ID == second[0].ID {
		t.Fatalf("last page did not contain the remaining original tenant: %+v", last)
	}
}

func TestMemPlatformTenantPageRejectsInvalidCursorAndOffsetCombination(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(context.Background(), "platform-tenant-cursor-invalid@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		offset int
		token  string
	}{
		{name: "malformed cursor", token: "not-a-cursor"},
		{name: "offset with cursor", offset: 1, token: "1:00000000000000000000000000000001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := store.ListPlatformTenantsPage(context.Background(), account.ID, 10, tc.offset, tc.token); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
