package state

import (
	"testing"
	"time"
)

func TestMemStoreAppLogDrainDeliveryAnalyticsRollsUpAndPrunes(t *testing.T) {
	store, ctx, account, app := appLogDrainFixture(t)
	drain, err := store.CreateAppLogDrain(ctx, AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/analytics-state", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain: %v", err)
	}
	now := time.Now().UTC()
	for _, health := range []AppLogDrainHealth{
		{DrainID: drain.ID, DeliveredTotal: 2, UpdatedAt: now.Add(-2 * time.Hour)},
		{DrainID: drain.ID, DeliveredTotal: 5, UpdatedAt: now.Add(-time.Hour)},
	} {
		if err := store.UpsertAppLogDrainHealth(ctx, health); err != nil {
			t.Fatalf("UpsertAppLogDrainHealth: %v", err)
		}
	}
	rows, err := store.ListAppLogDrainDeliveryAnalytics(ctx, drain.ID, now.Add(-3*time.Hour), now)
	if err != nil {
		t.Fatalf("ListAppLogDrainDeliveryAnalytics: %v", err)
	}
	if len(rows) != 2 || rows[0].DeliveredTotal != 2 || rows[1].DeliveredTotal != 5 {
		t.Fatalf("analytics rows = %+v", rows)
	}
	pruneBefore := now.Add(-2 * time.Hour).Truncate(time.Hour).Add(time.Minute)
	if err := store.PruneAppLogDrainDeliveryAnalytics(ctx, pruneBefore); err != nil {
		t.Fatalf("PruneAppLogDrainDeliveryAnalytics: %v", err)
	}
	rows, err = store.ListAppLogDrainDeliveryAnalytics(ctx, drain.ID, now.Add(-3*time.Hour), now)
	if err != nil {
		t.Fatalf("ListAppLogDrainDeliveryAnalytics after prune: %v", err)
	}
	if len(rows) != 1 || rows[0].DeliveredTotal != 5 {
		t.Fatalf("analytics rows after prune = %+v", rows)
	}
}
