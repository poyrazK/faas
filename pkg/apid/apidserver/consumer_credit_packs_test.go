package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/consumerbilling"
	"github.com/onebox-faas/faas/pkg/apid/statementinvoices"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

type creditPackStripe struct {
	mu           sync.Mutex
	post         url.Values
	idempotency  string
	starts       int
	status, paid string
	amount       int64
}

func (f *creditPackStripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoices":
		_, _ = fmt.Fprint(w, `{"data":[]}`)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
		_ = r.ParseForm()
		f.post, f.idempotency = r.PostForm, r.Header.Get("Idempotency-Key")
		f.starts++
	case r.Method == http.MethodGet && r.URL.Path == fmt.Sprintf("/v1/checkout/sessions/cs_test_packs_%d", f.starts):
	default:
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": fmt.Sprintf("cs_test_packs_%d", f.starts), "url": "https://checkout.stripe.test/pack", "mode": "payment", "customer": "cus_Buyer",
		"currency": "usd", "amount_total": f.amount, "status": f.status, "payment_status": f.paid,
		"client_reference_id": f.post.Get("client_reference_id"), "livemode": false,
	})
}

func (f *creditPackStripe) setPayment(status, paid string, amount int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.paid, f.amount = status, paid, amount
}

func TestConsumerCreditPackPurchase(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		withBillingKeys(t)
		e := apidserver.Setup(t, api.PlanHobby)
		s := *e.S()
		checkCreditPackPurchase(t, e.Do, *e.Store(), e.Acct().ID, s.ConsumerBilling(), s.StatementInvoices())
	})
	t.Run("postgres", func(t *testing.T) {
		withBillingKeys(t)
		e := apidserver.SetupPGHandler(t, api.PlanHobby)
		s := *e.S()
		checkCreditPackPurchase(t, e.Do, *e.Store(), e.Acct().ID, s.ConsumerBilling(), s.StatementInvoices())
	})
}

func checkCreditPackPurchase(t *testing.T, do meteringRequest, store state.Store, accountID string, service *consumerbilling.Service, invoices *statementinvoices.Service) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	service.Now = func() time.Time { return now }
	app, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: "packs", Type: state.AppTypeApp,
		Status: state.AppActive, MaxConcurrency: 1, PublicAuthMode: api.PlanHobby.PublicAuthModeDefault()})
	if err != nil {
		t.Fatal(err)
	}
	stripe := &creditPackStripe{status: "open", paid: "unpaid", amount: 2000}
	server := httptest.NewServer(stripe)
	t.Cleanup(server.Close)
	client := &stripeinvoice.Client{BaseURL: server.URL, HTTP: server.Client()}
	service.Stripe, invoices.Stripe = client, client
	meteringResponse(t, do, http.MethodPut, "/v1/account/billing-connections/stripe",
		api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, http.StatusOK, nil)

	createBuyer := func(ref string) (string, map[string]string) {
		t.Helper()
		var consumer api.APIConsumerResponse
		meteringResponse(t, do, http.MethodPost, "/v1/apps/packs/consumers",
			api.CreateAPIConsumerRequest{ExternalRef: ref, Name: ref}, http.StatusCreated, &consumer)
		var key api.ConsumerKeyResponse
		meteringResponse(t, do, http.MethodPost, "/v1/apps/packs/consumers/"+consumer.ID+"/keys",
			api.CreateConsumerKeyRequest{Name: "billing-" + ref, Scopes: []string{"billing"}}, http.StatusCreated, &key)
		return consumer.ID, map[string]string{"Authorization": "Bearer " + key.Key}
	}
	buyerID, buyer := createBuyer("cus_Buyer")
	_, other := createBuyer("cus_Other")
	request := func(method, path string, body any, headers map[string]string, status int, into any) {
		t.Helper()
		res := do(t, method, path, body, headers)
		if res.Code != status {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, res.Code, status, res.Body)
		}
		if into != nil {
			if err := json.Unmarshal(res.Body.Bytes(), into); err != nil {
				t.Fatal(err)
			}
		}
	}
	billingPath := "/v1/consumer-billing/packs"
	catalogPath := "/v1/apps/packs/consumer-credit-packs"
	purchases := billingPath + "/credit-purchases"
	req := api.CreateConsumerCreditPurchaseRequest{PurchaseID: uuid.NewString(), PackID: "starter"}
	request(http.MethodPost, purchases, req, buyer, http.StatusConflict, nil)

	const amount = 2_000_000 // USD 20.00 at 1,000 millicents per cent.
	settings := api.ConsumerCreditPackSettings{ReturnURL: "https://billing.example/credits?source=api",
		Packs: []api.ConsumerCreditPack{{ID: "starter", Label: "Starter credit", Currency: "USD", AmountMillicents: amount + 1}}}
	request(http.MethodPut, catalogPath, settings, nil, http.StatusUnprocessableEntity, nil)
	settings.Packs[0].AmountMillicents = amount
	request(http.MethodPut, catalogPath, settings, nil, http.StatusOK, nil)

	var offered api.ConsumerCreditPacksResponse
	request(http.MethodGet, billingPath+"/credit-packs", nil, buyer, http.StatusOK, &offered)
	if len(offered.Packs) != 1 || offered.Packs[0] != settings.Packs[0] {
		t.Fatalf("offered packs = %+v", offered)
	}
	var pending api.ConsumerCreditPurchaseResponse
	request(http.MethodPost, purchases, req, buyer, http.StatusOK, &pending)
	if pending.ID != req.PurchaseID || pending.Status != "pending" || pending.CheckoutURL == "" || pending.CreditGrantID != "" {
		t.Fatalf("pending purchase = %+v", pending)
	}
	request(http.MethodPost, purchases, req, buyer, http.StatusOK, &pending)
	request(http.MethodGet, purchases+"/"+req.PurchaseID, nil, other, http.StatusNotFound, nil)
	request(http.MethodPost, purchases, api.CreateConsumerCreditPurchaseRequest{PurchaseID: uuid.NewString(), PackID: req.PackID},
		buyer, http.StatusConflict, nil)
	checkStripeCreditCheckout(t, stripe, req.PurchaseID)

	// Disabling the catalog cannot strand a paid checkout.
	request(http.MethodPut, catalogPath, api.ConsumerCreditPackSettings{}, nil, http.StatusOK, nil)
	stripe.setPayment("complete", "unpaid", 2000)
	if err := service.SyncCreditPurchases(ctx); err != nil {
		t.Fatal(err)
	}
	checkPackGrants(t, store, app, buyerID, nil)

	now = now.Add(3 * time.Minute)
	stripe.setPayment("complete", "paid", 2001)
	if err := service.SyncCreditPurchases(ctx); err == nil {
		t.Fatal("accepted a paid checkout with the wrong amount")
	}
	checkPackGrants(t, store, app, buyerID, nil)

	caps := store.(state.APIConsumerSpendCapStore)
	checkedAt := time.Now().UTC().Add(-time.Minute)
	laterCheck := checkedAt.Add(15 * time.Minute)
	if _, err := caps.PutAPIConsumerSpendCap(ctx, state.APIConsumerSpendCap{
		AccountID: accountID, AppID: app.ID, ConsumerID: buyerID, Currency: "USD",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := caps.RecordAPIConsumerSpendCheck(ctx, buyerID, billing.MonthStart(checkedAt), 1000, true, checkedAt, laterCheck); err != nil {
		t.Fatal(err)
	}

	now = now.Add(3 * time.Minute)
	stripe.setPayment("complete", "paid", 2000)
	if err := service.SyncCreditPurchases(ctx); err != nil {
		t.Fatal(err)
	}
	var credited api.ConsumerCreditPurchaseResponse
	request(http.MethodGet, purchases+"/"+req.PurchaseID, nil, buyer, http.StatusOK, &credited)
	if credited.Status != "credited" || credited.CreditGrantID == "" || credited.CreditGrantID == credited.ID || credited.AmountMillicents != amount {
		t.Fatalf("credited purchase = %+v", credited)
	}
	if err := store.(state.APIConsumerCreditPackStore).CreditConsumerPurchase(ctx, credited.ID, "cs_test_packs_1"); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, purchases, req, buyer, http.StatusOK, &credited)
	checkPackGrants(t, store, app, buyerID, &credited)
	cap, err := caps.GetAPIConsumerSpendCap(ctx, accountID, buyerID)
	if err != nil || !cap.NextCheckAt.Before(laterCheck) || cap.UpdatedAt.Before(checkedAt) {
		t.Fatalf("spend cap was not scheduled for repricing: %+v, %v", cap, err)
	}

	request(http.MethodPut, catalogPath, settings, nil, http.StatusOK, nil)
	stripe.setPayment("open", "unpaid", 2000)
	canceled := api.CreateConsumerCreditPurchaseRequest{PurchaseID: uuid.NewString(), PackID: req.PackID}
	request(http.MethodPost, purchases, canceled, buyer, http.StatusOK, nil)
	stripe.setPayment("expired", "unpaid", 2000)
	if err := service.SyncCreditPurchases(ctx); err != nil {
		t.Fatal(err)
	}
	var expired api.ConsumerCreditPurchaseResponse
	request(http.MethodGet, purchases+"/"+canceled.PurchaseID, nil, buyer, http.StatusOK, &expired)
	if expired.Status != "expired" || expired.CreditGrantID != "" {
		t.Fatalf("expired purchase = %+v", expired)
	}
	checkPackGrants(t, store, app, buyerID, &credited)
}

func checkStripeCreditCheckout(t *testing.T, stripe *creditPackStripe, purchaseID string) {
	t.Helper()
	stripe.mu.Lock()
	defer stripe.mu.Unlock()
	if stripe.starts != 1 || stripe.idempotency != "gregale-credit-pack-"+purchaseID || stripe.post.Get("mode") != "payment" ||
		stripe.post.Get("customer") != "cus_Buyer" || stripe.post.Get("line_items[0][price_data][unit_amount]") != "2000" ||
		stripe.post.Get("success_url") != "https://billing.example/credits?credit_purchase_id="+purchaseID+"&source=api" {
		t.Fatalf("Stripe checkout = %d starts, key=%q, form=%v", stripe.starts, stripe.idempotency, stripe.post)
	}
}

func checkPackGrants(t *testing.T, store state.Store, app state.App, buyerID string, want *api.ConsumerCreditPurchaseResponse) {
	t.Helper()
	rows, err := store.(state.APIConsumerCreditStore).ListAPIConsumerCreditGrants(context.Background(), app.AccountID, app.ID, buyerID)
	if err != nil {
		t.Fatal(err)
	}
	if want == nil && len(rows) != 0 {
		t.Fatalf("expected no credit, got %+v", rows)
	}
	if want != nil && (len(rows) != 1 || rows[0].ID != want.CreditGrantID || rows[0].RemainingMillicents != want.AmountMillicents || rows[0].Currency != want.Currency) {
		t.Fatalf("grants = %+v, want purchase %+v", rows, want)
	}
}
