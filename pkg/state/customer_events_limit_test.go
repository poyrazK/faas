package state_test

import (
	"context"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreListCustomerEventsCapsLimit(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID := uuid.NewString()
	for range state.CustomerEventLimitMax + 1 {
		if err := store.AppendEvent(ctx, "test", "app.updated", &accountID, nil); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	rows, err := store.ListCustomerEvents(ctx, state.CustomerEventFilter{
		AccountID: accountID,
		Limit:     math.MaxInt,
	})
	if err != nil {
		t.Fatalf("ListCustomerEvents: %v", err)
	}
	if len(rows) != state.CustomerEventLimitMax {
		t.Fatalf("rows = %d, want capped limit %d", len(rows), state.CustomerEventLimitMax)
	}
}
