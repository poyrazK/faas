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

func creditsJSON[T any](t *testing.T, f billingRunFixture, method, path string, body any, want int) T {
	t.Helper()
	res := f.e.Do(t, method, path, body, nil)
	var out T
	if res.Code != want || (res.Body.Len() > 0 && json.Unmarshal(res.Body.Bytes(), &out) != nil) {
		t.Fatalf("%s %s: %d %s, want %d", method, path, res.Code, res.Body, want)
	}
	return out
}

// adr: 1306 — prepaid credit pays finalized statements oldest-expiring
// first and shows on exports as a credit line; a spend cap past the month's
// amount due after credit caps the consumer at the gateway until new credit
// lifts it.
func TestConsumerPrepaidCreditAndSpendCap(t *testing.T) {
	f := newBillingRunFixture(t)
	apidserver.MustSeedApp(t, f.e, "credits")
	if res := f.e.Do(t, http.MethodPost, "/v1/apps/credits/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, EffectiveFrom: &f.month}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	consumer := f.consumer(t, "credits", "cus_Prepaid1", 300, "") // last month: EUR 3.75
	base := "/v1/apps/credits/consumers/" + consumer.ID

	past := time.Now().Add(-time.Hour)
	creditsJSON[api.Problem](t, f, http.MethodPost, base+"/credits", api.CreateAPIConsumerCreditRequest{Currency: "eur", AmountMillicents: 1}, http.StatusUnprocessableEntity)
	creditsJSON[api.Problem](t, f, http.MethodPost, base+"/credits", api.CreateAPIConsumerCreditRequest{Currency: "EUR", AmountMillicents: 1, ExpiresAt: &past}, http.StatusUnprocessableEntity)
	soon := time.Now().Add(48 * time.Hour)
	expiring := creditsJSON[api.APIConsumerCreditResponse](t, f, http.MethodPost, base+"/credits",
		api.CreateAPIConsumerCreditRequest{Currency: "EUR", AmountMillicents: 100000, ExpiresAt: &soon, Note: "launch promo"}, http.StatusCreated)
	lasting := creditsJSON[api.APIConsumerCreditResponse](t, f, http.MethodPost, base+"/credits",
		api.CreateAPIConsumerCreditRequest{Currency: "EUR", AmountMillicents: 150000}, http.StatusCreated)
	if list := creditsJSON[api.APIConsumerCreditListResponse](t, f, http.MethodGet, base+"/credits", nil, http.StatusOK); len(list.Credits) != 2 ||
		len(list.Balances) != 1 || list.Balances[0].AvailableMillicents != 250000 {
		t.Fatalf("credits = %+v", list)
	}

	end := f.month.AddDate(0, 1, 0)
	statement := creditsJSON[api.APIConsumerUsageStatementResponse](t, f, http.MethodPost, base+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &f.month, PeriodEnd: &end}, http.StatusCreated)
	final := creditsJSON[api.APIConsumerUsageStatementResponse](t, f, http.MethodPost, base+"/usage-statements/"+statement.ID+"/finalize", struct{}{}, http.StatusOK)
	if final.AmountMillicents != 375000 || final.CreditAppliedMillicents != 250000 || final.AmountDueMillicents != 125000 {
		t.Fatalf("finalized statement = %+v", final)
	}
	list := creditsJSON[api.APIConsumerCreditListResponse](t, f, http.MethodGet, base+"/credits", nil, http.StatusOK)
	for _, c := range list.Credits {
		if (c.ID == expiring.ID || c.ID == lasting.ID) && (c.Status != api.ConsumerCreditUsed || c.RemainingMillicents != 0) {
			t.Fatalf("credit after finalize = %+v", c)
		}
	}
	export := creditsJSON[api.StatementInvoiceExportResponse](t, f, http.MethodPost, base+"/usage-statements/"+statement.ID+"/invoice", struct{}{}, http.StatusOK)
	if export.AmountMinor != 125 || len(f.stripe.amounts) != 2 || f.stripe.amounts[1] != "-250:Prepaid credit applied" {
		t.Fatalf("export = %+v, stripe items = %v", export, f.stripe.amounts)
	}

	// This month: 100 units (EUR 1.25) and no credit left; a zero cap
	// allows prepaid usage only.
	store := *f.e.Store()
	if _, err := store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(),
		AccountID: (*f.e.Acct()).ID, AppID: consumer.AppID, ConsumerKey: consumer.ID,
		WindowStart: time.Now().UTC().Truncate(time.Minute).Add(-time.Minute), RequestCount: 100, BillableUnits: 100}); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	spendCap := creditsJSON[api.APIConsumerSpendCapResponse](t, f, http.MethodPut, base+"/spend-cap",
		api.PutAPIConsumerSpendCapRequest{Currency: "EUR", MonthlyCapMillicents: &zero}, http.StatusOK)
	if !spendCap.Capped || spendCap.AmountDueMillicents == nil || *spendCap.AmountDueMillicents != 125000 {
		t.Fatalf("spend cap = %+v", spendCap)
	}
	policy := func() state.APIConsumerPlanPolicy {
		p, err := store.GetAPIConsumerPlanPolicy(context.Background(), (*f.e.Acct()).ID, consumer.AppID, consumer.ID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := policy(); !p.SpendCapped || p.SpendDueMillicents != 125000 || p.SpendCurrency != "EUR" {
		t.Fatalf("gateway policy while capped = %+v", p)
	}
	creditsJSON[api.APIConsumerCreditResponse](t, f, http.MethodPost, base+"/credits",
		api.CreateAPIConsumerCreditRequest{Currency: "EUR", AmountMillicents: 200000}, http.StatusCreated)
	if p := policy(); p.SpendCapped {
		t.Fatalf("new credit did not lift the cap: %+v", p)
	}
	service := (*f.e.S()).ConsumerCredits()
	service.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := service.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := creditsJSON[api.APIConsumerSpendCapResponse](t, f, http.MethodGet, base+"/spend-cap", nil, http.StatusOK); got.Capped ||
		*got.AmountDueMillicents != 0 {
		t.Fatalf("spend cap after worker check = %+v", got)
	}
	if res := f.e.Do(t, http.MethodDelete, base+"/spend-cap", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete spend cap: %d", res.Code)
	}
	voided := creditsJSON[api.APIConsumerCreditResponse](t, f, http.MethodDelete, base+"/credits/"+lasting.ID, nil, http.StatusOK)
	if voided.Status != api.ConsumerCreditVoided {
		t.Fatalf("voided = %+v", voided)
	}
}
