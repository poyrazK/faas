package apidserver_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1314 — a consumer created with a trial pays nothing for trial
// minutes; a trial cannot end in the past, and an ended one cannot restart.
func TestConsumerTrial(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "trial-api")
	if res := e.Do(t, http.MethodPost, "/v1/apps/trial-api/consumers", api.CreateAPIConsumerRequest{ExternalRef: "bad", Name: "Bad", TrialDays: 400}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("400-day trial: %d %s", res.Code, res.Body)
	}
	consumer := tenantJSON[api.APIConsumerResponse](t, e, http.MethodPost, "/v1/apps/trial-api/consumers",
		api.CreateAPIConsumerRequest{ExternalRef: "trialist", Name: "Trialist", TrialDays: 7}, http.StatusCreated)
	base := "/v1/apps/trial-api/consumers/" + consumer.ID
	trial := tenantJSON[api.APIConsumerTrialResponse](t, e, http.MethodGet, base+"/trial", nil, http.StatusOK)
	if !trial.Active || trial.EndsAt.Sub(trial.StartsAt) != 7*24*time.Hour {
		t.Fatalf("trial = %+v", trial)
	}
	past := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	tenantJSON[api.Problem](t, e, http.MethodPut, base+"/trial", api.PutAPIConsumerTrialRequest{EndsAt: &past}, http.StatusUnprocessableEntity)
	tenantJSON[api.Problem](t, e, http.MethodPut, base+"/trial", api.PutAPIConsumerTrialRequest{}, http.StatusUnprocessableEntity)
	longer := tenantJSON[api.APIConsumerTrialResponse](t, e, http.MethodPut, base+"/trial", api.PutAPIConsumerTrialRequest{Days: 14}, http.StatusOK)
	if !longer.StartsAt.Equal(trial.StartsAt) || longer.EndsAt.Sub(longer.StartsAt) != 14*24*time.Hour {
		t.Fatalf("extended trial = %+v", longer)
	}

	if res := e.Do(t, http.MethodPost, "/v1/apps/trial-api/rate-cards", api.CreateAPIConsumerRateCardRequest{Currency: "EUR",
		PriceMillicentsPerUnit: 100, EffectiveFrom: &trial.StartsAt}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	if _, err := (*e.Store()).RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(),
		AccountID: (*e.Acct()).ID, AppID: consumer.AppID, ConsumerKey: consumer.ID, WindowStart: trial.StartsAt,
		RequestCount: 5, BillableUnits: 5}); err != nil {
		t.Fatal(err)
	}
	end := trial.StartsAt.Add(time.Hour)
	statement := tenantJSON[api.APIConsumerUsageStatementResponse](t, e, http.MethodPost, base+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &trial.StartsAt, PeriodEnd: &end}, http.StatusCreated)
	if statement.BillableUnits != 5 || statement.AmountMillicents != 0 || len(statement.Buckets) != 1 || !statement.Buckets[0].Trial {
		t.Fatalf("trial statement = %+v", statement)
	}

	controls := (*e.S()).ConsumerControls()
	later := trial.StartsAt.AddDate(0, 0, 30)
	controls.Now = func() time.Time { return later }
	tenantJSON[api.Problem](t, e, http.MethodPut, base+"/trial", api.PutAPIConsumerTrialRequest{Days: 3}, http.StatusConflict)
}

// adr: 1315 — an app's policy blocks a consumer whose exported invoice is
// overdue long enough, its plan policy carries the hold for the gateway,
// and paying lifts it.
func TestConsumerOverdueHold(t *testing.T) {
	f := newBillingRunFixture(t)
	apidserver.MustSeedApp(t, f.e, "overdue")
	if res := f.e.Do(t, http.MethodPost, "/v1/apps/overdue/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, EffectiveFrom: &f.month}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	consumer := f.consumer(t, "overdue", "cus_Late1", 300, "")
	run := f.drain(t, f.start(t, api.CreateBillingRunRequest{Month: f.month.Format("2006-01"), Export: "stripe"}).ID)
	if run.Counts.Exported != 1 {
		t.Fatalf("run counts = %+v", run.Counts)
	}
	payments := (*f.e.S()).StatementInvoices()
	now := time.Now().UTC().Add(2 * time.Hour)
	payments.Now = func() time.Time { return now }
	due := now.AddDate(0, 0, -20)
	f.stripe.setPayment("in_1", fmt.Sprintf(`{"id":"in_1","status":"open","amount_paid":0,"amount_remaining":375,"due_date":%d,"status_transitions":{"paid_at":null}}`, due.Unix()))
	if err := payments.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	tenantJSON[api.Problem](t, f.e, http.MethodPut, "/v1/apps/overdue/consumer-billing-policy", api.PutAPIConsumerBillingPolicyRequest{}, http.StatusUnprocessableEntity)
	thirty, fourteen := 30, 14
	tenantJSON[api.APIConsumerBillingPolicyResponse](t, f.e, http.MethodPut, "/v1/apps/overdue/consumer-billing-policy",
		api.PutAPIConsumerBillingPolicyRequest{BlockOverdueAfterDays: &thirty}, http.StatusOK)
	controls := (*f.e.S()).ConsumerControls()
	controls.Now = func() time.Time { return now }
	if err := controls.SyncOverdueOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if holds := tenantJSON[api.APIConsumerOverdueHoldListResponse](t, f.e, http.MethodGet, "/v1/apps/overdue/consumer-overdue-holds", nil, http.StatusOK); len(holds.Holds) != 0 {
		t.Fatalf("20 days overdue under a 30-day policy: holds = %+v", holds)
	}
	tenantJSON[api.APIConsumerBillingPolicyResponse](t, f.e, http.MethodPut, "/v1/apps/overdue/consumer-billing-policy",
		api.PutAPIConsumerBillingPolicyRequest{BlockOverdueAfterDays: &fourteen}, http.StatusOK)
	if err := controls.SyncOverdueOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	holds := tenantJSON[api.APIConsumerOverdueHoldListResponse](t, f.e, http.MethodGet, "/v1/apps/overdue/consumer-overdue-holds", nil, http.StatusOK)
	if len(holds.Holds) != 1 || holds.Holds[0].ConsumerID != consumer.ID {
		t.Fatalf("holds = %+v", holds)
	}
	plans := *f.e.Store()
	policy, err := plans.GetAPIConsumerPlanPolicy(context.Background(), (*f.e.Acct()).ID, consumer.AppID, consumer.ID, now)
	if err != nil || !policy.OverdueBlocked || policy.OverdueDueAt.Unix() != due.Unix() {
		t.Fatalf("plan policy = %+v, %v", policy, err)
	}

	now = now.Add(time.Hour)
	f.stripe.setPayment("in_1", fmt.Sprintf(`{"id":"in_1","status":"paid","amount_paid":375,"amount_remaining":0,"due_date":%d,"status_transitions":{"paid_at":%d}}`, due.Unix(), now.Unix()))
	if err := payments.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := controls.SyncOverdueOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if policy, err := plans.GetAPIConsumerPlanPolicy(context.Background(), (*f.e.Acct()).ID, consumer.AppID, consumer.ID, now); err != nil || policy.OverdueBlocked {
		t.Fatalf("after payment: %+v, %v", policy, err)
	}
	if got := tenantJSON[api.APIConsumerBillingPolicyResponse](t, f.e, http.MethodGet, "/v1/apps/overdue/consumer-billing-policy", nil, http.StatusOK); got.BlockOverdueAfterDays != 14 {
		t.Fatalf("policy = %+v", got)
	}
	if res := f.e.Do(t, http.MethodDelete, "/v1/apps/overdue/consumer-billing-policy", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete policy: %d %s", res.Code, res.Body)
	}
}
