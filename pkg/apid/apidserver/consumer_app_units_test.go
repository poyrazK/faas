package apidserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1317, 1318 — a card with unit app:tokens prices the tokens the app
// reported, beside the request card, with its own included units.
func TestConsumerAppReportedUnits(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "tokens")
	consumer := tenantJSON[api.APIConsumerResponse](t, e, http.MethodPost, "/v1/apps/tokens/consumers",
		api.CreateAPIConsumerRequest{ExternalRef: "llm-user", Name: "LLM user"}, http.StatusCreated)
	from := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	cards := "/v1/apps/tokens/rate-cards"
	for _, req := range map[string]api.CreateAPIConsumerRateCardRequest{
		"bad meter":       {Currency: "EUR", PriceMillicentsPerUnit: 1, Unit: "app:Tokens!", EffectiveFrom: &from},
		"weighted tokens": {Currency: "EUR", PriceMillicentsPerUnit: 1, Unit: "app:tokens", RouteWeights: map[string]int64{"GET /a": 2}, EffectiveFrom: &from},
	} {
		tenantJSON[api.Problem](t, e, http.MethodPost, cards, req, http.StatusUnprocessableEntity)
	}
	tenantJSON[api.APIConsumerRateCardResponse](t, e, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{Currency: "EUR",
		PriceMillicentsPerUnit: 10, EffectiveFrom: &from}, http.StatusCreated)
	tokens := tenantJSON[api.APIConsumerRateCardResponse](t, e, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{Currency: "EUR",
		PriceMillicentsPerUnit: 2, IncludedUnitsPerMonth: 1000, Unit: "app:tokens", EffectiveFrom: &from}, http.StatusCreated)
	if tokens.Unit != "app:tokens" {
		t.Fatalf("tokens card = %+v", tokens)
	}

	minute := from.Add(time.Minute)
	if _, err := (*e.Store()).RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(),
		AccountID: (*e.Acct()).ID, AppID: consumer.AppID, ConsumerKey: consumer.ID, WindowStart: minute,
		RequestCount: 3, BillableUnits: 3, MeterUnits: map[string]int64{"tokens": 1500, "images": 4}}); err != nil {
		t.Fatal(err)
	}
	end := minute.Add(time.Hour)
	statement := tenantJSON[api.APIConsumerUsageStatementResponse](t, e, http.MethodPost, "/v1/apps/tokens/consumers/"+consumer.ID+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &minute, PeriodEnd: &end}, http.StatusCreated)
	// 3 requests at 10, and 1500 tokens of which 1000 are included: 500 at 2.
	if len(statement.Buckets) != 2 || statement.Buckets[1].Unit != "app:tokens" || statement.Buckets[1].BillableUnits != 1500 ||
		statement.Buckets[1].ChargedUnits != 500 || statement.AmountMillicents != 1030 {
		t.Fatalf("statement = %+v", statement)
	}
}
