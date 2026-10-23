package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1506 — a consumer key with the billing scope reads its own month to
// date, daily usage and finalized statements; other keys are refused.
func TestConsumerBillingSelfService(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "metered")
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	(*e.S()).ConsumerBilling().Now = func() time.Time { return now }
	if res := e.Do(t, http.MethodPost, "/v1/apps/metered/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, EffectiveFrom: &monthStart}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	created := e.Do(t, http.MethodPost, "/v1/apps/metered/consumers", api.CreateAPIConsumerRequest{ExternalRef: "cus_Portal1", Name: "Portal"}, nil)
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("consumer: %d %s", created.Code, created.Body)
	}
	key := func(scopes ...string) string {
		t.Helper()
		res := e.Do(t, http.MethodPost, "/v1/apps/metered/consumers/"+consumer.ID+"/keys", api.CreateConsumerKeyRequest{Name: "k" + strings.Join(scopes, ""), Scopes: scopes}, nil)
		var out api.ConsumerKeyResponse
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || res.Code != http.StatusCreated || out.Key == "" {
			t.Fatalf("key-create %v: %d %s", scopes, res.Code, res.Body)
		}
		return out.Key
	}
	billingKey, appKey := key("read", "billing"), key("read")
	minute := now.Add(-time.Minute).Truncate(time.Minute)
	if minute.Before(monthStart) {
		minute = monthStart
	}
	if _, err := (*e.Store()).RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(),
		AccountID: (*e.Acct()).ID, AppID: consumer.AppID, ConsumerKey: consumer.ID, WindowStart: minute, RequestCount: 4, BillableUnits: 3}); err != nil {
		t.Fatal(err)
	}
	get := func(path, token string) *apidserverResponse {
		res := e.Do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + token})
		return &apidserverResponse{code: res.Code, body: res.Body.String()}
	}

	for name, token := range map[string]string{"operator key": *e.Key(), "malformed": "ck_nope", "wrong secret": billingKey[:len(billingKey)-1] + "0"} {
		if res := get("/v1/consumer-billing/metered", token); res.code != http.StatusUnauthorized {
			t.Fatalf("%s: %d %s, want 401", name, res.code, res.body)
		}
	}
	if res := get("/v1/consumer-billing/metered", appKey); res.code != http.StatusForbidden {
		t.Fatalf("key without billing scope: %d %s, want 403", res.code, res.body)
	}

	res := get("/v1/consumer-billing/metered", billingKey)
	var summary api.ConsumerBillingSummaryResponse
	if err := json.Unmarshal([]byte(res.body), &summary); err != nil || res.code != http.StatusOK {
		t.Fatalf("summary: %d %s", res.code, res.body)
	}
	if summary.ConsumerID != consumer.ID || summary.BillableUnits != 3 || summary.AmountMillicents != 3750 || summary.Currency != "EUR" ||
		summary.Month != monthStart.Format("2006-01") || summary.App != "metered" {
		t.Fatalf("summary = %+v", summary)
	}

	res = get("/v1/consumer-billing/metered/usage", billingKey)
	var usage api.ConsumerBillingUsageResponse
	if err := json.Unmarshal([]byte(res.body), &usage); err != nil || res.code != http.StatusOK || len(usage.Days) != 1 ||
		usage.Days[0].Requests != 4 || usage.Days[0].BillableUnits != 3 {
		t.Fatalf("usage: %d %s", res.code, res.body)
	}
	if res := get("/v1/consumer-billing/metered/usage?since=2026-01-01T00:00:00Z&until=2026-03-01T00:00:00Z", billingKey); res.code != http.StatusBadRequest {
		t.Fatalf("62-day window: %d %s, want 400", res.code, res.body)
	}

	end := now.Truncate(time.Minute).Add(time.Minute)
	draft := e.Do(t, http.MethodPost, "/v1/apps/metered/consumers/"+consumer.ID+"/usage-statements", api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &monthStart, PeriodEnd: &end}, nil)
	var statement api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(draft.Body.Bytes(), &statement); err != nil || draft.Code != http.StatusCreated {
		t.Fatalf("statement: %d %s", draft.Code, draft.Body)
	}
	if res := get("/v1/consumer-billing/metered/statements", billingKey); res.code != http.StatusOK || strings.Contains(res.body, statement.ID) {
		t.Fatalf("draft statements are hidden: %d %s", res.code, res.body)
	}
	if _, err := (*e.Store()).CreateAPIConsumerCreditGrant(context.Background(), state.APIConsumerCreditGrant{
		AccountID: (*e.Acct()).ID, AppID: consumer.AppID, ConsumerID: consumer.ID, Currency: "EUR", AmountMillicents: 1000}); err != nil {
		t.Fatal(err)
	}
	if fin := e.Do(t, http.MethodPost, "/v1/apps/metered/consumers/"+consumer.ID+"/usage-statements/"+statement.ID+"/finalize", struct{}{}, nil); fin.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", fin.Code, fin.Body)
	}
	res = get("/v1/consumer-billing/metered/statements", billingKey)
	var statements api.ConsumerBillingStatementsResponse
	if err := json.Unmarshal([]byte(res.body), &statements); err != nil || res.code != http.StatusOK || len(statements.Statements) != 1 ||
		statements.Statements[0].ID != statement.ID || statements.Statements[0].AmountMillicents != 3750 ||
		statements.Statements[0].CreditAppliedMillicents != 1000 || statements.Statements[0].AmountDueMillicents != 2750 {
		t.Fatalf("statements: %d %s", res.code, res.body)
	}

	if rev := e.Do(t, http.MethodDelete, "/v1/apps/metered/consumers/"+consumer.ID, nil, nil); rev.Code >= 300 {
		t.Fatalf("revoke consumer: %d %s", rev.Code, rev.Body)
	}
	if res := get("/v1/consumer-billing/metered", billingKey); res.code != http.StatusUnauthorized {
		t.Fatalf("revoked consumer: %d %s, want 401", res.code, res.body)
	}
}

type apidserverResponse struct {
	code int
	body string
}
