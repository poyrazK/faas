package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1308 — a compute_second card prices a consumer's billable compute in
// whole seconds per minute; units are validated and change only at a month
// boundary.
func TestConsumerComputePricing(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "metered")
	created := e.Do(t, http.MethodPost, "/v1/apps/metered/consumers", api.CreateAPIConsumerRequest{ExternalRef: "gpu-customer", Name: "GPU customer"}, nil)
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("consumer: %d %s", created.Code, created.Body)
	}
	cards := "/v1/apps/metered/rate-cards"
	from := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	past := from.Add(-time.Hour)
	for name, req := range map[string]api.CreateAPIConsumerRateCardRequest{
		"unknown unit":      {Currency: "EUR", PriceMillicentsPerUnit: 1, Unit: "gpu_hour", EffectiveFrom: &from},
		"weighted compute":  {Currency: "EUR", PriceMillicentsPerUnit: 1, Unit: "compute_second", RouteWeights: map[string]int64{"GET /a": 2}, EffectiveFrom: &from},
		"backdated compute": {Currency: "EUR", PriceMillicentsPerUnit: 1, Unit: "compute_second", EffectiveFrom: &past},
	} {
		if res := e.Do(t, http.MethodPost, cards, req, nil); res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s, want 422", name, res.Code, res.Body)
		}
	}
	res := e.Do(t, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{Currency: "EUR", PriceMillicentsPerUnit: 100,
		Unit: "compute_second", EffectiveFrom: &from}, nil)
	var card api.APIConsumerRateCardResponse
	if err := json.Unmarshal(res.Body.Bytes(), &card); err != nil || res.Code != http.StatusCreated || card.Unit != "compute_second" {
		t.Fatalf("compute card: %d %s", res.Code, res.Body)
	}

	minute := from.Add(time.Minute)
	for _, ms := range []int64{400, 700, 900} {
		if _, err := (*e.Store()).RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(),
			AccountID: (*e.Acct()).ID, AppID: consumer.AppID, ConsumerKey: consumer.ID, WindowStart: minute,
			RequestCount: 1, BillableUnits: 1, ComputeMS: ms}); err != nil {
			t.Fatal(err)
		}
	}
	end := minute.Add(time.Hour)
	res = e.Do(t, http.MethodPost, "/v1/apps/metered/consumers/"+consumer.ID+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &minute, PeriodEnd: &end}, nil)
	var statement api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(res.Body.Bytes(), &statement); err != nil || res.Code != http.StatusCreated {
		t.Fatalf("statement: %d %s", res.Code, res.Body)
	}
	// 2,000 ms of billable compute is 2 seconds at 100 millicents.
	if statement.BillableUnits != 2 || statement.AmountMillicents != 200 {
		t.Fatalf("statement = %+v", statement)
	}

	// adr: 1317 — a request card added mid-month prices requests alongside
	// compute instead of replacing it: one bucket per unit and minute.
	later := from.Add(5 * time.Minute)
	if res := e.Do(t, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{Currency: "EUR", PriceMillicentsPerUnit: 1,
		EffectiveFrom: &later}, nil); res.Code != http.StatusCreated {
		t.Fatalf("request card beside compute: %d %s", res.Code, res.Body)
	}
	at := later.Add(time.Minute)
	if _, err := (*e.Store()).RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(),
		AccountID: (*e.Acct()).ID, AppID: consumer.AppID, ConsumerKey: consumer.ID, WindowStart: at,
		RequestCount: 2, BillableUnits: 2, ComputeMS: 3000}); err != nil {
		t.Fatal(err)
	}
	until := at.Add(time.Minute)
	res = e.Do(t, http.MethodPost, "/v1/apps/metered/consumers/"+consumer.ID+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &at, PeriodEnd: &until}, nil)
	var both api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(res.Body.Bytes(), &both); err != nil || res.Code != http.StatusCreated {
		t.Fatalf("two-unit statement: %d %s", res.Code, res.Body)
	}
	// 2 requests at 1 and 3 compute seconds at 100.
	if len(both.Buckets) != 2 || both.Buckets[0].Unit != "" || both.Buckets[1].Unit != "compute_second" || both.AmountMillicents != 302 {
		t.Fatalf("two-unit statement = %+v", both)
	}
}
