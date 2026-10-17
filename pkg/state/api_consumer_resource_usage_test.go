package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkConsumerResourceUsage runs the ADR-1308 store contract: usage
// minutes sum billable compute and response size, anonymous or unbillable
// events cannot carry them, and cards store their unit.
func checkConsumerResourceUsage(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	consumer, err := st.CreateAPIConsumer(ctx, accountID, appID, "metered", "Metered")
	if err != nil {
		t.Fatal(err)
	}
	usage := st.(state.ConsumerUsageStore)
	minute := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	for _, ms := range []int64{700, 900} {
		if _, err := usage.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID,
			AppID: appID, ConsumerKey: consumer.ID, WindowStart: minute, RequestCount: 1, BillableUnits: 1,
			ComputeMS: ms, ResponseBytes: 300_000}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := usage.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID,
		AppID: appID, ConsumerKey: consumer.ID, WindowStart: minute, RequestCount: 1, ComputeMS: 5}); err == nil {
		t.Fatal("an unbillable event carried compute")
	}
	buckets, err := usage.ListAPIConsumerUsage(ctx, accountID, appID, consumer.ID, minute, minute.Add(time.Minute))
	if err != nil || len(buckets) != 1 || buckets[0].ComputeMS != 1600 || buckets[0].ResponseBytes != 600_000 || buckets[0].BillableUnits != 2 {
		t.Fatalf("buckets = %+v, %v", buckets, err)
	}
	cards := st.(state.APIConsumerRateCardStore)
	card, err := cards.CreateAPIConsumerRateCardVersion(ctx, state.APIConsumerRateCardInput{AccountID: accountID, AppID: appID,
		Currency: "EUR", PriceMillicentsPerUnit: 10, Unit: state.APIConsumerRateCardUnitComputeSecond,
		EffectiveFrom: time.Now().UTC().Truncate(time.Minute).Add(time.Minute)})
	if err != nil || card.Unit != state.APIConsumerRateCardUnitComputeSecond {
		t.Fatalf("compute card = %+v, %v", card, err)
	}
	if _, err := cards.CreateAPIConsumerRateCardVersion(ctx, state.APIConsumerRateCardInput{AccountID: accountID, AppID: appID,
		Currency: "EUR", Unit: state.APIConsumerRateCardUnitResponseMegabyte, RouteWeights: map[string]int64{"GET /a": 2},
		EffectiveFrom: time.Now().UTC().Truncate(time.Minute).Add(2 * time.Minute)}); err == nil {
		t.Fatal("route weights accepted on a response-size card")
	}
}

// adr: 1308
func TestMemStoreConsumerResourceUsage(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkConsumerResourceUsage(t, ctx, store, accountID, appID)
}
