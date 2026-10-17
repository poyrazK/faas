package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/lagoinvoice"
	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
)

// fakePaddle keeps transactions like Paddle Billing would.
type fakePaddle struct {
	mu      sync.Mutex
	created []map[string]any
	status  map[string]string // transaction ID -> GET response
}

func (f *fakePaddle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer pdl_sdbx_apikey_") || r.Header.Get("Paddle-Version") != "1" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"error":{"code":"forbidden","detail":"bad key"}}`)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/transactions":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body)
		_, _ = fmt.Fprintf(w, `{"data":{"id":"txn_%d","status":"draft"}}`, len(f.created))
	case r.Method == http.MethodGet && r.URL.Path == "/transactions":
		var data []string
		for i, body := range f.created {
			custom, _ := json.Marshal(body["custom_data"])
			data = append(data, fmt.Sprintf(`{"id":"txn_%d","custom_data":%s}`, i+1, custom))
		}
		_, _ = fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(data, ","))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/transactions/"):
		body, ok := f.status[strings.TrimPrefix(r.URL.Path, "/transactions/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error":{"code":"not_found","detail":"no such entity"}}`)
			return
		}
		_, _ = fmt.Fprint(w, body)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// fakeLago keeps one-off invoices like Lago would. failCreate stores the
// next invoice but answers 502, like a crash after Lago issued it.
type fakeLago struct {
	mu         sync.Mutex
	addOn      bool
	invoices   []lagoFakeInvoice
	failCreate bool
}

type lagoFakeInvoice struct {
	body          map[string]any
	status        string
	paymentStatus string
	exportID      string
}

func (f *fakeLago) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer lago-key-1234" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"status":401,"error":"Unauthorized"}`)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	switch {
	case r.Method == http.MethodGet && path == "/add_ons":
		_, _ = fmt.Fprint(w, `{"add_ons":[]}`)
	case r.Method == http.MethodGet && path == "/add_ons/gregale_usage" && !f.addOn:
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"status":404,"error":"Not Found","code":"add_on_not_found"}`)
	case path == "/add_ons/gregale_usage" || (r.Method == http.MethodPost && path == "/add_ons"):
		f.addOn = true
		_, _ = fmt.Fprint(w, `{"add_on":{"code":"gregale_usage"}}`)
	case r.Method == http.MethodPost && path == "/invoices":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.invoices = append(f.invoices, lagoFakeInvoice{body: body, status: "finalized", paymentStatus: "pending"})
		if f.failCreate {
			f.failCreate = false
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprintf(w, `{"invoice":{"lago_id":"inv-%d","status":"finalized"}}`, len(f.invoices))
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/invoices/"):
		var body struct {
			Invoice struct {
				Metadata []struct{ Key, Value string } `json:"metadata"`
			} `json:"invoice"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		inv := f.invoice(strings.TrimPrefix(path, "/invoices/"))
		for _, m := range body.Invoice.Metadata {
			if m.Key == "gregale_export_id" && inv != nil {
				inv.exportID = m.Value
			}
		}
		_, _ = fmt.Fprint(w, `{"invoice":{}}`)
	case r.Method == http.MethodGet && path == "/invoices":
		var data []string
		for i, inv := range f.invoices {
			if want := r.URL.Query().Get("metadata[gregale_export_id]"); want != "" && inv.exportID != want {
				continue
			}
			data = append(data, f.invoiceJSON(i))
		}
		_, _ = fmt.Fprintf(w, `{"invoices":[%s]}`, strings.Join(data, ","))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/invoices/"):
		for i := range f.invoices {
			if fmt.Sprintf("/invoices/inv-%d", i+1) == path {
				_, _ = fmt.Fprintf(w, `{"invoice":%s}`, f.invoiceJSON(i))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"status":404,"error":"Not Found","code":"invoice_not_found"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeLago) invoice(lagoID string) *lagoFakeInvoice {
	for i := range f.invoices {
		if fmt.Sprintf("inv-%d", i+1) == lagoID {
			return &f.invoices[i]
		}
	}
	return nil
}

func (f *fakeLago) invoiceJSON(i int) string {
	inv := f.invoices[i]
	fees := 0
	for _, fee := range inv.body["invoice"].(map[string]any)["fees"].([]any) {
		fees += int(fee.(map[string]any)["unit_amount_cents"].(float64))
	}
	metadata := "[]"
	if inv.exportID != "" {
		metadata = fmt.Sprintf(`[{"key":"gregale_export_id","value":%q}]`, inv.exportID)
	}
	paid := 0
	if inv.paymentStatus == "succeeded" {
		paid = fees
	}
	return fmt.Sprintf(`{"lago_id":"inv-%d","invoice_type":"one_off","status":%q,"payment_status":%q,"payment_due_date":"2026-11-01",
		"fees_amount_cents":%d,"total_amount_cents":%d,"total_paid_amount_cents":%d,"total_due_amount_cents":%d,
		"web_url":"https://lago.example.com/inv-%d","updated_at":"2026-10-14T00:00:00Z","metadata":%s}`,
		i+1, inv.status, inv.paymentStatus, fees, fees, paid, fees-paid, i+1, metadata)
}

func exportStatement(t *testing.T, f billingRunFixture, path string, req api.CreateStatementInvoiceRequest, want int) api.StatementInvoiceExportResponse {
	t.Helper()
	res := f.e.Do(t, http.MethodPost, path, req, nil)
	var out api.StatementInvoiceExportResponse
	if res.Code != want || (want == http.StatusOK && json.Unmarshal(res.Body.Bytes(), &out) != nil) {
		t.Fatalf("export %s via %s: %d %s, want %d", path, req.Provider, res.Code, res.Body, want)
	}
	return out
}

// adr: 1507 — billing runs and statement exports target Paddle and Lago:
// Paddle gets a manual draft transaction, Lago an issued one-off invoice; a
// statement exports to one provider only, a Lago invoice an interrupted
// export left untagged blocks a second one, and payment status syncs back.
func TestStatementInvoiceExportsToPaddleAndLago(t *testing.T) {
	f := newBillingRunFixture(t)
	paddle, lago := &fakePaddle{status: map[string]string{}}, &fakeLago{}
	paddleSrv, lagoSrv := httptest.NewServer(paddle), httptest.NewTLSServer(lago)
	defer paddleSrv.Close()
	defer lagoSrv.Close()
	service := (*f.e.S()).StatementInvoices()
	service.Paddle = &paddleinvoice.Client{BaseURL: paddleSrv.URL, HTTP: paddleSrv.Client()}
	service.Lago = &lagoinvoice.Client{HTTP: lagoSrv.Client()}

	connections := map[string]struct {
		req  api.PutBillingConnectionRequest
		want int
	}{
		"paddle":    {api.PutBillingConnectionRequest{SecretKey: "pdl_sdbx_apikey_01abcdWXYZ"}, http.StatusOK},
		"lago":      {api.PutBillingConnectionRequest{SecretKey: "lago-key-1234", APIURL: lagoSrv.URL + "/api/v1/"}, http.StatusOK},
		"chargebee": {api.PutBillingConnectionRequest{SecretKey: "cb_key_1234"}, http.StatusNotFound},
	}
	for provider, c := range connections {
		if res := f.e.Do(t, http.MethodPut, "/v1/account/billing-connections/"+provider, c.req, nil); res.Code != c.want {
			t.Fatalf("connect %s: %d %s", provider, res.Code, res.Body)
		}
	}
	if res := f.e.Do(t, http.MethodPut, "/v1/account/billing-connections/paddle", api.PutBillingConnectionRequest{
		SecretKey: "pdl_sdbx_apikey_01abcdWXYZ", APIURL: "https://api.paddle.com"}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("paddle api_url: %d %s", res.Code, res.Body)
	}
	if res := f.e.Do(t, http.MethodGet, "/v1/account/billing-connections/lago", nil, nil); res.Code != http.StatusOK ||
		!strings.Contains(res.Body.String(), `"api_url":"`+lagoSrv.URL+`"`) || strings.Contains(res.Body.String(), "lago-key") {
		t.Fatalf("lago connection: %d %s", res.Code, res.Body)
	}

	apidserver.MustSeedApp(t, f.e, "priced")
	if res := f.e.Do(t, http.MethodPost, "/v1/apps/priced/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, EffectiveFrom: &f.month}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	paying := f.consumer(t, "priced", "ctm_01paying", 300, "")
	acme := f.consumer(t, "priced", "acme-co", 200, "")
	run := f.drain(t, f.start(t, api.CreateBillingRunRequest{Month: f.month.Format("2006-01"), Export: "paddle"}).ID)
	items := f.items(t, run.ID)
	if run.Counts.Exported != 1 || items["ctm_01paying"].InvoiceID != "txn_1" ||
		items["acme-co"].Outcome != "failed" || !strings.Contains(items["acme-co"].Reason, "Paddle customer") {
		t.Fatalf("paddle run = %+v, items = %+v", run.Counts, items)
	}
	txn := paddle.created[0]
	price := txn["items"].([]any)[0].(map[string]any)["price"].(map[string]any)
	if txn["collection_mode"] != "manual" || txn["customer_id"] != "ctm_01paying" || price["unit_price"].(map[string]any)["amount"] != "375" ||
		txn["custom_data"].(map[string]any)["gregale_export_id"] == "" {
		t.Fatalf("paddle transaction = %v", txn)
	}

	statements := "/v1/apps/priced/consumers/"
	payingPath := statements + paying.ID + "/usage-statements/" + items["ctm_01paying"].StatementID + "/invoice"
	exportStatement(t, f, payingPath, api.CreateStatementInvoiceRequest{Provider: "lago", Customer: "acme-co"}, http.StatusConflict)
	if replay := exportStatement(t, f, payingPath, api.CreateStatementInvoiceRequest{Provider: "paddle"}, http.StatusOK); replay.InvoiceID != "txn_1" ||
		!strings.Contains(replay.DashboardURL, "sandbox-vendors.paddle.com") || len(paddle.created) != 1 {
		t.Fatalf("paddle replay = %+v, transactions = %d", replay, len(paddle.created))
	}

	acmePath := statements + acme.ID + "/usage-statements/" + items["acme-co"].StatementID + "/invoice"
	lago.failCreate = true
	exportStatement(t, f, acmePath, api.CreateStatementInvoiceRequest{Provider: "lago"}, http.StatusBadGateway)
	res := f.e.Do(t, http.MethodPost, acmePath, api.CreateStatementInvoiceRequest{Provider: "lago"}, nil)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "inv-1") {
		t.Fatalf("export after an untagged Lago invoice: %d %s, want 409 naming inv-1", res.Code, res.Body)
	}
	lago.invoice("inv-1").status = "voided"
	export := exportStatement(t, f, acmePath, api.CreateStatementInvoiceRequest{Provider: "lago"}, http.StatusOK)
	created := lago.invoices[1].body["invoice"].(map[string]any)
	fee := created["fees"].([]any)[0].(map[string]any)
	if export.InvoiceID != "inv-2" || export.Customer != "acme-co" || export.Provider != "lago" || created["skip_psp"] != true ||
		fee["unit_amount_cents"] != float64(250) || fee["add_on_code"] != "gregale_usage" || lago.invoices[1].exportID == "" || !lago.addOn {
		t.Fatalf("lago export = %+v, invoice = %v", export, lago.invoices[1])
	}

	now := time.Now().UTC().Add(2 * time.Hour)
	service.Now = func() time.Time { return now }
	billed := now.AddDate(0, 0, -40).Format(time.RFC3339)
	paddle.status["txn_1"] = `{"data":{"id":"txn_1","status":"past_due","currency_code":"EUR","billed_at":"` + billed + `",
		"details":{"totals":{"grand_total":"375","balance":"375"}},"billing_details":{"payment_terms":{"interval":"day","frequency":30}},
		"checkout":{"url":"https://pay.example.com/txn_1"},"payments":[]}}`
	lago.invoice("inv-2").paymentStatus = "succeeded"
	if err := service.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := listInvoices(t, f, "overdue"); len(got.Invoices) != 1 || got.Invoices[0].Provider != "paddle" ||
		*got.Invoices[0].AmountRemainingMinor != 375 || got.Invoices[0].HostedInvoiceURL != "https://pay.example.com/txn_1" {
		t.Fatalf("overdue = %+v", got)
	}
	if got := listInvoices(t, f, "paid"); len(got.Invoices) != 1 || got.Invoices[0].Provider != "lago" ||
		got.Invoices[0].HostedInvoiceURL != "https://lago.example.com/inv-2" || got.Invoices[0].DashboardURL != "" {
		t.Fatalf("paid = %+v", got)
	}
}
