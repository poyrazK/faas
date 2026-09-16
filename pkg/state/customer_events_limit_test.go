package state

import (
	"context"
	"math"
	"testing"

	"github.com/google/uuid"
)

func TestMemStoreListCustomerEventsCapsLimit(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	accountID := uuid.NewString()
	for range CustomerEventLimitMax + 1 {
		if err := store.AppendEvent(ctx, "test", "app.updated", &accountID, nil); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	rows, err := store.ListCustomerEvents(ctx, CustomerEventFilter{
		AccountID: accountID,
		Limit:     math.MaxInt,
	})
	if err != nil {
		t.Fatalf("ListCustomerEvents: %v", err)
	}
	if len(rows) != CustomerEventLimitMax {
		t.Fatalf("rows = %d, want capped limit %d", len(rows), CustomerEventLimitMax)
	}
}
