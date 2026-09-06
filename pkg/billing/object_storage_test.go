package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFinalizeObjectStoragePeriodIsClosedAndIdempotent(t *testing.T) {
	store := state.NewMemStore()
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	pricing := api.ObjectStoragePricing{Currency: "EUR", StorageMillicentsPerGiBMonth: 1000}
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	first, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, period, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, period, now.Add(time.Hour))
	if err != nil || second.ID != first.ID || second.TotalMillicents != first.TotalMillicents {
		t.Fatalf("retry = %#v, err=%v", second, err)
	}
	if _, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, state.ObjectStoragePeriod(now), now); !errors.Is(err, state.ErrObjectBillingOpen) {
		t.Fatalf("open period error = %v", err)
	}
}
