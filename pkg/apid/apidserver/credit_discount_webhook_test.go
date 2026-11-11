package apidserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
)

const creditDiscountWebhookSecret = "polar_whs_private-credit-discount-test-secret"

// adr: 20261104212028
// A Polar order that carries Gregale's credit discount draws what Polar
// applied from the account's credit once, whether or not it is paid; a
// redelivery draws nothing more, and the rest of the credit is attached to
// the subscription again for the next order.
func TestPolarOrderWithCreditDiscountDrawsTheCreditOnce(t *testing.T) {
	ctx := t.Context()
	attachedAgain := make(chan int64, 4)
	polarAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/subscriptions/sub-1":
			_, _ = io.WriteString(w, `{"id":"sub-1","status":"active","currency":"usd","discount_id":"disc-1","current_period_end":"2099-01-01T00:00:00Z"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/discounts":
			var body struct {
				Amounts map[string]int64 `json:"amounts"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			attachedAgain <- body.Amounts["usd"]
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"disc-2"}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(polarAPI.Close)

	e := apidserver.Setup(t, api.PlanPro)
	s := *e.S()
	provider, err := polar.NewProvider(polar.Config{APIKey: "private-offline-credit-discount-test-token",
		WebhookSecret: creditDiscountWebhookSecret, BaseURL: polarAPI.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.WithBillingProvider(provider)
	h, store := s.Handler(), *e.Store()
	acct := e.Acct()
	customer := "polar-credit-" + acct.ID
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: customer, SubscriptionID: "sub-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAccountCredit(ctx, state.AccountCredit{AccountID: acct.ID, CentsRemaining: 1000, Reason: "goodwill"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCreditDiscount(ctx, state.CreditDiscount{SubjectID: acct.ID, Provider: "polar",
		ProviderSubscriptionID: "sub-1", ProviderDiscountID: "disc-1", AmountCents: 1000, Currency: "usd"}); err != nil {
		t.Fatal(err)
	}

	deliver := func() int {
		t.Helper()
		body, err := json.Marshal(map[string]any{"type": "order.created", "data": map[string]any{
			"id": "ord-1", "customer_id": customer, "subscription_id": "sub-1", "status": "pending",
			"currency": "usd", "subtotal_amount": 600, "discount_amount": 600, "total_amount": 0, "discount_id": "disc-1",
		}})
		if err != nil {
			t.Fatal(err)
		}
		when, id := time.Now(), uuid.NewString()
		r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/polar", bytes.NewReader(body))
		r.Header.Set("webhook-id", id)
		r.Header.Set("webhook-timestamp", strconv.FormatInt(when.Unix(), 10))
		r.Header.Set("webhook-signature", polar.SignForTest(body, creditDiscountWebhookSecret, id, when))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	remaining := func() int64 {
		t.Helper()
		credits, err := store.ListActiveCreditsForConsumption(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		var total int64
		for _, c := range credits {
			total += c.CentsRemaining
		}
		return total
	}

	if code := deliver(); code != http.StatusOK || remaining() != 400 {
		t.Fatalf("order delivery = %d, credit left %d; want 200 and 400", code, remaining())
	}
	select {
	case amount := <-attachedAgain:
		if amount != 400 {
			t.Fatalf("discount attached after the draw = %d, want the 400 left", amount)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the rest of the credit was not attached again")
	}
	if code := deliver(); code != http.StatusOK || remaining() != 400 {
		t.Fatalf("redelivery = %d, credit left %d; want 200 and still 400", code, remaining())
	}
	redeemed, err := store.CreditDiscountByProviderID(ctx, "polar", "disc-1")
	if err != nil || redeemed.State != state.CreditDiscountRedeemed || redeemed.RedeemedCents != 600 {
		t.Fatalf("discount = %+v, %v; want redeemed for 600", redeemed, err)
	}
}
