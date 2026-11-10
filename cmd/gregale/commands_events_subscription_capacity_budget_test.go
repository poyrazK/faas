package main_test

import (
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestSubscriptionCapacityBudgetToleratesClockSkew pins Hunt 32 H32-74: a
// CLI clock a fraction of a second behind the server must not reject every
// receipt budget as future-dated.
func TestSubscriptionCapacityBudgetToleratesClockSkew(t *testing.T) {
	now := time.Date(2026, 11, 10, 10, 54, 0, 0, time.UTC)
	const app, sub = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	for _, tc := range []struct {
		name     string
		observed time.Time
		wantErr  bool
	}{
		{"server 300ms ahead", now.Add(300 * time.Millisecond), false},
		{"server at the skew bound", now.Add(api.EventDeliveryObservationClockSkew), false},
		{"server beyond the skew bound", now.Add(api.EventDeliveryObservationClockSkew + time.Millisecond), true},
		{"fresh", now.Add(-time.Second), false},
		{"stale", now.Add(-api.EventDeliveryHealthWatchAgeDefault - time.Second), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &api.EventSubscriptionCapacityCommandBudget{
				AppID: app, SubscriptionID: sub, ObservedAt: tc.observed, Complete: true,
				Limit: api.EventSubscriptionCapacityCommandMaxReceipts, RemainingSlots: api.EventSubscriptionCapacityCommandMaxReceipts,
			}
			err := main.ValidateSubscriptionCapacityCommandBudget(b, app, sub, now, api.EventDeliveryHealthWatchAgeDefault)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %t", err, tc.wantErr)
			}
		})
	}
}
