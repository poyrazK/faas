package apidserver_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1316 — a card's base fee and minimum bill every consumer's month,
// with usage or without; a billing run finds idle consumers and exports the
// fees as their own invoice items.
func TestConsumerMonthlyFees(t *testing.T) {
	f := newBillingRunFixture(t)
	next := billing.MonthStart(time.Now().UTC()).AddDate(0, 1, 0)
	(*f.e.S()).BillingRuns().Now = func() time.Time { return next.AddDate(0, 1, 2) }
	apidserver.MustSeedApp(t, f.e, "fees")
	if res := f.e.Do(t, http.MethodPost, "/v1/apps/fees/rate-cards", api.CreateAPIConsumerRateCardRequest{Currency: "EUR",
		PriceMillicentsPerUnit: 1250, BaseFeeMillicentsPerMonth: -1, EffectiveFrom: &next}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("negative base fee: %d %s", res.Code, res.Body)
	}
	card := tenantJSON[api.APIConsumerRateCardResponse](t, f.e, http.MethodPost, "/v1/apps/fees/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, BaseFeeMillicentsPerMonth: 500_000, MinimumMillicentsPerMonth: 1_000_000,
		EffectiveFrom: &next}, http.StatusCreated)
	if card.BaseFeeMillicentsPerMonth != 500_000 || card.MinimumMillicentsPerMonth != 1_000_000 {
		t.Fatalf("card = %+v", card)
	}
	active := tenantJSON[api.APIConsumerResponse](t, f.e, http.MethodPost, "/v1/apps/fees/consumers",
		api.CreateAPIConsumerRequest{ExternalRef: "cus_Active1", Name: "Active"}, http.StatusCreated)
	tenantJSON[api.APIConsumerResponse](t, f.e, http.MethodPost, "/v1/apps/fees/consumers",
		api.CreateAPIConsumerRequest{ExternalRef: "cus_Idle1", Name: "Idle"}, http.StatusCreated)
	if _, err := (*f.e.Store()).RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(),
		AccountID: (*f.e.Acct()).ID, AppID: active.AppID, ConsumerKey: active.ID, WindowStart: next.Add(time.Hour),
		RequestCount: 3, BillableUnits: 3}); err != nil {
		t.Fatal(err)
	}

	week := next.AddDate(0, 0, 7)
	tenantJSON[api.Problem](t, f.e, http.MethodPost, "/v1/apps/fees/consumers/"+active.ID+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &next, PeriodEnd: &week}, http.StatusUnprocessableEntity)

	run := f.drain(t, f.start(t, api.CreateBillingRunRequest{Month: next.Format("2006-01"), Export: "stripe"}).ID)
	items := f.items(t, run.ID)
	// 3 units at 1250 plus the 5.00 base fee and a 9.9625 top-up to the
	// 10.00 minimum; the idle consumer pays the fee and the whole minimum.
	for _, ref := range []string{"cus_Active1", "cus_Idle1"} {
		if item := items[ref]; item.AmountMillicents == nil || *item.AmountMillicents != 1_500_000 || item.InvoiceID == "" {
			t.Fatalf("%s item = %+v (run %+v)", ref, item, run.Counts)
		}
	}
	for _, want := range []string{"4:3 usage units at 0.0125 EUR each", "500:Monthly base fee", "996:Minimum monthly spend top-up", "1000:Minimum monthly spend top-up"} {
		if !slices.Contains(f.stripe.amounts, want) {
			t.Fatalf("stripe items = %v, missing %q", f.stripe.amounts, want)
		}
	}
	statement := tenantJSON[api.APIConsumerUsageStatementResponse](t, f.e, http.MethodGet,
		"/v1/apps/fees/consumers/"+active.ID+"/usage-statements/"+items["cus_Active1"].StatementID, nil, http.StatusOK)
	if len(statement.Fees) != 2 || statement.Fees[0].Kind != "base_fee" || statement.Fees[1].AmountMillicents != 996_250 {
		t.Fatalf("statement fees = %+v", statement.Fees)
	}
}
