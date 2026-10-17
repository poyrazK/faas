package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/lagoinvoice"
)

// finalizedMonthStatement drafts and finalizes a consumer's statement for
// the fixture's month and returns its export path.
func finalizedMonthStatement(t *testing.T, f billingRunFixture, slug, consumerID string) string {
	t.Helper()
	statements := "/v1/apps/" + slug + "/consumers/" + consumerID + "/usage-statements"
	end := f.month.AddDate(0, 1, 0)
	res := f.e.Do(t, http.MethodPost, statements, api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &f.month, PeriodEnd: &end}, nil)
	var statement api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(res.Body.Bytes(), &statement); err != nil || res.Code != http.StatusCreated {
		t.Fatalf("statement: %d %s", res.Code, res.Body)
	}
	if res := f.e.Do(t, http.MethodPost, statements+"/"+statement.ID+"/finalize", struct{}{}, nil); res.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", res.Code, res.Body)
	}
	return statements + "/" + statement.ID + "/invoice"
}

// adr: 1311 — a connection set to collect has Stripe finalize each export so
// it charges or emails it, and Lago charge it; modes a provider cannot do
// are refused.
func TestStatementInvoiceCollection(t *testing.T) {
	f := newBillingRunFixture(t)
	f.stripe.trackDrafts = true
	lago := &fakeLago{}
	lagoSrv := httptest.NewTLSServer(lago)
	defer lagoSrv.Close()
	(*f.e.S()).StatementInvoices().Lago = &lagoinvoice.Client{HTTP: lagoSrv.Client()}
	lagoURL := lagoSrv.URL + "/api/v1/"

	for name, c := range map[string]struct {
		provider string
		req      api.PutBillingConnectionRequest
	}{
		"paddle charge":     {"paddle", api.PutBillingConnectionRequest{SecretKey: "pdl_sdbx_apikey_01abcdWXYZ", Collection: "charge"}},
		"lago send":         {"lago", api.PutBillingConnectionRequest{SecretKey: "lago-key-1234", APIURL: lagoURL, Collection: "send"}},
		"days without send": {"stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ", Collection: "charge", DaysUntilDue: 7}},
		"unknown mode":      {"stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ", Collection: "dun"}},
		"lago tax":          {"lago", api.PutBillingConnectionRequest{SecretKey: "lago-key-1234", APIURL: lagoURL, AutomaticTax: true}},
	} {
		if res := f.e.Do(t, http.MethodPut, "/v1/account/billing-connections/"+c.provider, c.req, nil); res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s, want 422", name, res.Code, res.Body)
		}
	}
	res := f.e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ",
		Collection: "charge", AutomaticTax: true}, nil)
	var conn api.BillingConnectionResponse
	if err := json.Unmarshal(res.Body.Bytes(), &conn); err != nil || res.Code != http.StatusOK || conn.Collection != "charge" || conn.DaysUntilDue != 30 ||
		!conn.AutomaticTax {
		t.Fatalf("connect charge: %d %s", res.Code, res.Body)
	}

	apidserver.MustSeedApp(t, f.e, "collected")
	if res := f.e.Do(t, http.MethodPost, "/v1/apps/collected/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, EffectiveFrom: &f.month}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	charged := f.consumer(t, "collected", "cus_CHARGE1", 300, "")
	run := f.drain(t, f.start(t, api.CreateBillingRunRequest{Month: f.month.Format("2006-01"), Export: "stripe"}).ID)
	items := f.items(t, run.ID)
	if run.Counts.Exported != 1 || items["cus_CHARGE1"].InvoiceID != "in_1" ||
		f.stripe.collection["in_1"] != "charge_automatically/" || f.stripe.finalized["in_1"] != "true" {
		t.Fatalf("charge run = %+v, collection = %v, finalized = %v", run.Counts, f.stripe.collection, f.stripe.finalized)
	}
	chargedPath := "/v1/apps/collected/consumers/" + charged.ID + "/usage-statements/" + items["cus_CHARGE1"].StatementID + "/invoice"
	if export := exportStatement(t, f, chargedPath, api.CreateStatementInvoiceRequest{}, http.StatusOK); export.Collection != "charge" || !export.AutomaticTax {
		t.Fatalf("charged export = %+v", export)
	}

	if res := f.e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{
		SecretKey: "rk_test_51abcdWXYZ", Collection: "send", DaysUntilDue: 14}, nil); res.Code != http.StatusOK {
		t.Fatalf("connect send: %d %s", res.Code, res.Body)
	}
	sent := f.consumer(t, "collected", "cus_SEND1", 200, "")
	sentPath := finalizedMonthStatement(t, f, "collected", sent.ID)
	f.stripe.failNextFinalize = true
	exportStatement(t, f, sentPath, api.CreateStatementInvoiceRequest{}, http.StatusBadGateway)
	export := exportStatement(t, f, sentPath, api.CreateStatementInvoiceRequest{}, http.StatusOK)
	if export.Collection != "send" || export.Status != "completed" || f.stripe.collection["in_2"] != "send_invoice/14" ||
		f.stripe.finalized["in_2"] != "true" || f.stripe.invoices != 2 {
		t.Fatalf("sent export = %+v, collection = %v, finalized = %v", export, f.stripe.collection, f.stripe.finalized)
	}

	if res := f.e.Do(t, http.MethodPut, "/v1/account/billing-connections/lago", api.PutBillingConnectionRequest{
		SecretKey: "lago-key-1234", APIURL: lagoURL, Collection: "charge"}, nil); res.Code != http.StatusOK {
		t.Fatalf("connect lago: %d %s", res.Code, res.Body)
	}
	acme := f.consumer(t, "collected", "acme-co", 100, "")
	lagoExport := exportStatement(t, f, finalizedMonthStatement(t, f, "collected", acme.ID), api.CreateStatementInvoiceRequest{Provider: "lago"}, http.StatusOK)
	created := lago.invoices[0].body["invoice"].(map[string]any)
	if lagoExport.Collection != "charge" || created["skip_psp"] != false {
		t.Fatalf("lago export = %+v, invoice = %v", lagoExport, created)
	}
}
