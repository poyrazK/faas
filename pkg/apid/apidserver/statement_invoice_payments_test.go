package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func listInvoices(t *testing.T, f billingRunFixture, status string) api.StatementInvoiceListResponse {
	t.Helper()
	res := f.e.Do(t, http.MethodGet, "/v1/account/statement-invoices?status="+status, nil, nil)
	var out api.StatementInvoiceListResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || res.Code != http.StatusOK {
		t.Fatalf("list %s invoices: %d %s", status, res.Code, res.Body)
	}
	return out
}

// adr: 1504 — exported invoices follow Stripe until paid, unpaid and overdue
// lists show them, a run needing attention emails the account, and the daily
// digest reports an overdue invoice once.
func TestStatementInvoicePaymentStatusAndNotifications(t *testing.T) {
	f := newBillingRunFixture(t)
	mailer := &apidserver.RecordingMailer{}
	*(*f.e.S()).Mailer() = mailer
	apidserver.MustSeedApp(t, f.e, "priced")
	apidserver.MustSeedApp(t, f.e, "unpriced")
	if res := f.e.Do(t, http.MethodPost, "/v1/apps/priced/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, EffectiveFrom: &f.month}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	f.consumer(t, "priced", "cus_Paying1", 300, "")
	f.consumer(t, "unpriced", "cus_Unpriced1", 4, "")
	run := f.drain(t, f.start(t, api.CreateBillingRunRequest{Month: f.month.Format("2006-01"), Export: "stripe"}).ID)
	if run.Counts.Exported != 1 || run.Counts.Held != 1 {
		t.Fatalf("run counts = %+v", run.Counts)
	}
	sent := mailer.Snapshot()
	if len(sent) != 1 || !strings.Contains(sent[0].Subject, "needs attention: 1 held, 0 failed") || !strings.Contains(sent[0].TextBody, run.ID) {
		t.Fatalf("run notification = %+v", sent)
	}

	if got := listInvoices(t, f, "unpaid"); len(got.Invoices) != 1 || got.Invoices[0].PaymentStatus != "draft" {
		t.Fatalf("unpaid before sync = %+v", got)
	}
	service := (*f.e.S()).StatementInvoices()
	now := time.Now().UTC().Add(2 * time.Hour)
	service.Now = func() time.Time { return now }
	due := now.AddDate(0, 0, -3)
	f.stripe.setPayment("in_1", fmt.Sprintf(`{"id":"in_1","status":"open","amount_paid":0,"amount_remaining":375,"due_date":%d,"hosted_invoice_url":"https://invoice.stripe.com/i/in_1","status_transitions":{"paid_at":null}}`, due.Unix()))
	if err := service.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	overdue := listInvoices(t, f, "overdue")
	if len(overdue.Invoices) != 1 || !overdue.Invoices[0].Overdue || overdue.Invoices[0].PaymentStatus != "open" ||
		overdue.Invoices[0].AmountRemainingMinor == nil || *overdue.Invoices[0].AmountRemainingMinor != 375 ||
		overdue.Invoices[0].HostedInvoiceURL == "" || overdue.Invoices[0].SourceKind != "app_consumer" {
		t.Fatalf("overdue = %+v", overdue)
	}
	sent = mailer.Snapshot()
	if len(sent) != 2 || !strings.Contains(sent[1].Subject, "overdue") || !strings.Contains(sent[1].TextBody, "in_1") ||
		!strings.Contains(sent[1].TextBody, "EUR 3.75 remaining") {
		t.Fatalf("overdue digest = %+v", sent)
	}
	if err := service.SyncOnce(context.Background()); err != nil || len(mailer.Snapshot()) != 2 {
		t.Fatalf("second tick: %v, emails %d; the digest reports an invoice once", err, len(mailer.Snapshot()))
	}

	now = now.Add(7 * time.Hour)
	f.stripe.setPayment("in_1", fmt.Sprintf(`{"id":"in_1","status":"paid","amount_paid":375,"amount_remaining":0,"due_date":%d,"status_transitions":{"paid_at":%d}}`, due.Unix(), now.Unix()))
	if err := service.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := listInvoices(t, f, "paid"); len(got.Invoices) != 1 || got.Invoices[0].PaidAt == nil || got.Invoices[0].Overdue {
		t.Fatalf("paid = %+v", got)
	}
	if got := listInvoices(t, f, "unpaid"); len(got.Invoices) != 0 {
		t.Fatalf("unpaid after payment = %+v", got)
	}
	if item := f.items(t, run.ID)["cus_Paying1"]; item.PaymentStatus != "paid" {
		t.Fatalf("billing run item = %+v", item)
	}
	if res := f.e.Do(t, http.MethodGet, "/v1/account/statement-invoices?status=owed", nil, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("unknown status: %d", res.Code)
	}
}
