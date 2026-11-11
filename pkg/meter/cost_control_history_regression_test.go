package meter

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCostControlPricingRetainsPlanRoundTripWhileMeterDown(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "audit-plan-roundtrip@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	before := month.Add(9*24*time.Hour + 9*time.Hour)
	freeAt := before.Add(time.Hour)
	paidAgain := freeAt.Add(2 * time.Hour)
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: "audit-customer", SubscriptionID: "audit-subscription", BillingFrom: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{store: store}
	if err := loop.recordFinancialPricing(ctx, before); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	store.SetPlanChangedAt(acct.ID, freeAt)
	// meterd is down while Free becomes active and the subject resubscribes.
	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	store.SetPlanChangedAt(acct.ID, paidAgain)
	loop = &Loop{store: store} // a restarted process has no pricing cache
	if err := loop.recordFinancialPricing(ctx, paidAgain.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Catch-up writes usage for the hours preceding the restart.
	for _, row := range []struct {
		at       time.Time
		quantity int64
	}{{before, api.IncludedMBSeconds(api.PlanHobby)}, {freeAt, 10 * api.SecondsPerGBHour}} {
		if err := store.AppendUsage(ctx, acct.ID, uuid.NewString(), uuid.NewString(), row.at, row.quantity, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	prices, err := store.ListFinancialPriceSnapshots(ctx, acct.ID, month)
	if err != nil {
		t.Fatal(err)
	}
	observedPlan := state.MonthPlanAt(api.PlanHobby, prices, freeAt.Add(time.Hour))
	var mu sync.Mutex
	var accepted int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events/ingest" {
			t.Errorf("unexpected provider path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body struct {
			Events []struct {
				Metadata struct {
					MBSeconds int64 `json:"mb_seconds"`
				} `json:"metadata"`
			} `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		for _, event := range body.Events {
			accepted += event.Metadata.MBSeconds
		}
		mu.Unlock()
		w.Write([]byte(`{"inserted":1,"duplicates":0}`))
	}))
	defer server.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	provider, err := polar.NewProviderWithDedupe(polar.Config{APIKey: "audit-local", BaseURL: server.URL}, logger, store)
	if err != nil {
		t.Fatal(err)
	}
	// All usage hours are older than the sampler catch-up/finality bound.
	now := paidAgain.Add(api.MeterUsageFinalAfter + 2*time.Hour)
	pusher := NewPusher(store, provider, logger, func() time.Time { return now }, nil)
	pushed, err := pusher.PushPending(ctx, api.MeterUsageFinalAfter+24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retained snapshots=%d; observed Free-period plan=%s; pushed positive windows=%d", len(prices), observedPlan, pushed)
	mu.Lock()
	defer mu.Unlock()
	if observedPlan != api.PlanFree || accepted != 0 {
		t.Errorf("Hobby -> Free -> Hobby between pricing passes lost Free history: %d retained snapshots, Free hour classified as %s, provider receives %d GB-h of overage for Free usage", len(prices), observedPlan, accepted/api.SecondsPerGBHour)
	}
}
