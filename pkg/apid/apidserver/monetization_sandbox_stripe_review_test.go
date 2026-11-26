//go:build monetization_sandbox

package apidserver_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMonetizationSandboxStripeReviewedInvoice(t *testing.T) {
	key := sandboxStripeKey(t)
	client := stripeinvoice.New()
	calls := &sandboxStripeIssueCalls{next: http.DefaultTransport}
	client.HTTP.Transport = calls
	stripe := sandboxHTTP{t: t, base: client.BaseURL, form: true, header: http.Header{
		"Authorization": {"Bearer " + key}, "Stripe-Version": {stripeinvoice.APIVersion},
	}}
	customer := createSandboxStripeCustomer(stripe, true)
	verifyReviewedStripeBillingMonths(t, client, calls, stripe, key, customer)
}

func verifyReviewedStripeBillingMonths(t *testing.T, client *stripeinvoice.Client, calls *sandboxStripeIssueCalls,
	stripe sandboxHTTP, key string, customer sandboxStripeCustomer) {
	t.Helper()
	f := newSandboxFixture(t)
	server := *f.e.S()
	server.StatementInvoices().Stripe = client
	f.connect("stripe", api.PutBillingConnectionRequest{SecretKey: key})
	lastClosed := f.month
	firstMonth := lastClosed.AddDate(0, -1, 0)
	server.BillingRuns().Now = func() time.Time { return lastClosed.AddDate(0, 1, 2) }
	f.month = firstMonth // Make the rate card effective in both months.
	slug := f.app("EUR", 1250)
	appPath := "/v1/apps/" + slug
	store := *f.e.Store()
	consumer := func() api.APIConsumerResponse {
		store.SetClockForTest(func() time.Time { return firstMonth })
		defer store.SetClockForTest(nil)
		return sandboxJSON[api.APIConsumerResponse](f, http.MethodPost, appPath+"/consumers",
			api.CreateAPIConsumerRequest{ExternalRef: customer.ID, Name: "Sandbox Stripe customer"}, http.StatusCreated)
	}()
	if !consumer.CreatedAt.Equal(firstMonth) {
		t.Fatalf("customer registered at %v, want start of first billing month %v", consumer.CreatedAt, firstMonth)
	}
	consumerPath := appPath + "/consumers/" + consumer.ID
	sandboxJSON[api.APIConsumerCreditResponse](f, http.MethodPost, consumerPath+"/credits",
		api.CreateAPIConsumerCreditRequest{Currency: "EUR", AmountMillicents: 125_000}, http.StatusCreated)

	cycles := []struct {
		month     time.Time
		units     int64
		amountDue int64 // EUR cents after credit
	}{
		{firstMonth, 400, 375},
		{lastClosed, 500, 625},
	}
	registerCleanup := func(id string) {
		t.Cleanup(func() { cleanReviewedSandboxInvoice(stripe, client, key, id) })
	}
	seen := make(map[string]string, len(cycles)) // Stripe invoice -> statement
	for _, cycle := range cycles {
		t.Logf("qualifying reviewed billing for %s UTC", cycle.month.Format("2006-01"))
		if _, err := store.RecordAPIConsumerUsage(t.Context(), state.APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: (*f.e.Acct()).ID, AppID: consumer.AppID,
			ConsumerKey: consumer.ID, WindowStart: cycle.month.Add(time.Hour),
			RequestCount: cycle.units, BillableUnits: cycle.units,
		}); err != nil {
			t.Fatal(err)
		}
		draft := reviewedStripeMonthDraft(f, consumer, consumerPath, cycle.month, cycle.amountDue, registerCleanup)
		if _, duplicate := seen[draft.InvoiceID]; duplicate {
			t.Fatalf("billing month reused Stripe invoice %s", draft.InvoiceID)
		}
		for _, statementID := range seen {
			if statementID == draft.StatementID {
				t.Fatalf("billing month reused statement %s", draft.StatementID)
			}
		}
		invoicePath := consumerPath + "/usage-statements/" + draft.StatementID + "/invoice"
		verifyReviewedStripePayment(f, client, calls, stripe, key, customer, invoicePath, draft, cycle.amountDue)
		seen[draft.InvoiceID] = draft.StatementID

		funnel := sandboxJSON[apidserver.APIMonetizationFunnelResponse](f, http.MethodGet, appPath+"/monetization-funnel", nil, http.StatusOK)
		if funnel.TotalRegistered != 1 || funnel.Registered != 1 || funnel.PricedInFirst30Days != 1 ||
			funnel.LiveInvoiceSent != 0 || funnel.LivePayment != 0 || funnel.NextBillableMonth != 0 ||
			funnel.NextMonthLivePayment != 0 || len(funnel.SecondInvoiceReview) != 0 {
			t.Fatalf("funnel after %s test payment: %+v", cycle.month.Format("2006-01"), funnel)
		}
	}
	var invoices struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		HasMore bool `json:"has_more"`
	}
	query := url.Values{"customer": {customer.ID}, "limit": {"3"}}
	if code := stripe.call(http.MethodGet, "/v1/invoices?"+query.Encode(), nil, &invoices); code != http.StatusOK ||
		invoices.HasMore || len(invoices.Data) != len(cycles) {
		t.Fatalf("Stripe customer invoices across both months: HTTP %d, %+v", code, invoices)
	}
	for _, invoice := range invoices.Data {
		if _, ok := seen[invoice.ID]; !ok {
			t.Fatalf("Stripe listed unexpected or duplicate invoice %s", invoice.ID)
		}
		delete(seen, invoice.ID)
	}
}

func reviewedStripeMonthDraft(f *sandboxFixture, consumer api.APIConsumerResponse, consumerPath string,
	month time.Time, amountDue int64, registerCleanup func(string)) api.StatementInvoiceExportResponse {
	t := f.t
	t.Helper()
	finalize := false
	runner := billingRunFixture{e: f.e}
	run := runner.start(t, api.CreateBillingRunRequest{
		Month: month.Format("2006-01"), Sources: []string{state.BillingRunSourceAppConsumers}, Finalize: &finalize,
	})
	run = runner.drain(t, run.ID)
	if run.Counts.Drafted != 1 || run.Counts.Held != 0 || run.Counts.Failed != 0 ||
		!run.PeriodStart.Equal(month) || !run.PeriodEnd.Equal(month.AddDate(0, 1, 0)) {
		t.Fatalf("closed billing month did not produce one draft: %+v", run)
	}
	path := "/v1/account/billing-runs/" + run.ID + "/draft-exports"
	review := sandboxJSON[api.BillingRunDraftExportReview](f, http.MethodGet, path, nil, http.StatusOK)
	if len(review.Items) != 1 || review.NextCursor != "" || review.Livemode == nil ||
		*review.Livemode || review.ConnectionUpdatedAt == nil {
		t.Fatalf("unexpected Stripe draft review: %+v", review)
	}
	row := review.Items[0]
	if row.Status != "ready" || row.ConsumerID != consumer.ID || row.StripeCustomer != consumer.ExternalRef ||
		row.StatementID == "" || row.Currency != "EUR" || row.AmountDueMillicents == nil ||
		*row.AmountDueMillicents != amountDue*1000 {
		t.Fatalf("unexpected reviewed draft: %+v", row)
	}
	approval := api.ApproveBillingRunDraftExportRequest{
		ConsumerID: row.ConsumerID, StatementID: row.StatementID, Revision: row.Revision,
		Currency: row.Currency, AmountDueMillicents: *row.AmountDueMillicents, StripeCustomer: row.StripeCustomer,
		Livemode: review.Livemode, ConnectionUpdatedAt: *review.ConnectionUpdatedAt,
	}
	draft := sandboxJSON[api.StatementInvoiceExportResponse](f, http.MethodPost, path, approval, http.StatusOK)
	if draft.InvoiceID != "" {
		registerCleanup(draft.InvoiceID)
	}
	replayed := sandboxJSON[api.StatementInvoiceExportResponse](f, http.MethodPost, path, approval, http.StatusOK)
	if draft.StatementID != row.StatementID || draft.InvoiceID == "" || replayed.InvoiceID != draft.InvoiceID {
		t.Fatalf("draft approval changed the Stripe invoice: %+v, replay %+v", draft, replayed)
	}
	statement := sandboxJSON[api.APIConsumerUsageStatementResponse](f, http.MethodGet,
		consumerPath+"/usage-statements/"+draft.StatementID, nil, http.StatusOK)
	if statement.Status != string(state.APIConsumerUsageStatementFinalized) || statement.ConsumerID != consumer.ID ||
		!statement.PeriodStart.Equal(month) || !statement.PeriodEnd.Equal(month.AddDate(0, 1, 0)) {
		t.Fatalf("Stripe draft does not match the closed monthly statement: %+v", statement)
	}
	return draft
}

func verifyReviewedStripePayment(f *sandboxFixture, client *stripeinvoice.Client, calls *sandboxStripeIssueCalls,
	stripe sandboxHTTP, key string, customer sandboxStripeCustomer, invoicePath string,
	draft api.StatementInvoiceExportResponse, amountDue int64) {
	t := f.t
	t.Helper()
	beforeFinalize, beforeSend := calls.finalize.Load(), calls.send.Load()
	rawPath := "/v1/invoices/" + url.PathEscape(draft.InvoiceID)

	readInvoice := func() stripeinvoice.InvoiceState {
		t.Helper()
		invoice, err := client.GetInvoice(t.Context(), key, draft.InvoiceID)
		if err != nil {
			t.Fatal(err)
		}
		if invoice.ID != draft.InvoiceID || invoice.Customer != customer.ID ||
			invoice.Livemode == nil || *invoice.Livemode || invoice.Currency != "eur" {
			t.Fatalf("unexpected Stripe invoice: %+v", invoice)
		}
		return invoice
	}
	if invoice := readInvoice(); invoice.Status != state.InvoicePaymentDraft || invoice.AmountRemaining != amountDue ||
		invoice.AutoAdvance == nil || *invoice.AutoAdvance || draft.Collection != state.CollectionNone ||
		draft.AmountMinor != amountDue || draft.Currency != "EUR" || draft.Livemode {
		t.Fatalf("draft sent to Stripe = %+v, draft export = %+v", invoice, draft)
	}
	consumerPath, ok := strings.CutSuffix(invoicePath, "/usage-statements/"+draft.StatementID+"/invoice")
	if !ok || draft.StatementID == "" {
		t.Fatalf("draft export does not match its statement: %+v", draft)
	}

	sendPath := "/v1/account/statement-invoices/" + draft.StatementID + "/send"
	review := sandboxJSON[api.StripeInvoiceSendReview](f, http.MethodGet, sendPath, nil, http.StatusOK)
	if review.InvoiceID != draft.InvoiceID || review.StripeCustomer != customer.ID || review.RecipientEmail != customer.Email ||
		review.AmountDueMinor != amountDue || review.Currency != "EUR" || review.Livemode ||
		review.ReviewToken == "" || !review.DueAt.After(time.Now()) {
		t.Fatalf("send review = %+v, want the test customer and %d EUR cents due in the future", review, amountDue)
	}
	if calls.finalize.Load() != beforeFinalize || calls.send.Load() != beforeSend {
		t.Fatal("Stripe invoice was finalized or sent before approval")
	}
	approve := api.SendStripeInvoiceRequest{ReviewToken: review.ReviewToken}
	sent := sandboxJSON[api.StatementInvoiceExportResponse](f, http.MethodPost, sendPath, approve, http.StatusOK)
	if sent.InvoiceID != draft.InvoiceID || sent.Livemode || sent.PaymentStatus != state.InvoicePaymentOpen ||
		sent.StripeIssuedAt == nil || sent.StripeSentAt == nil || sent.StripeSendPending ||
		sent.DueAt == nil || !sent.DueAt.Equal(review.DueAt) ||
		sent.AmountRemainingMinor == nil || *sent.AmountRemainingMinor != amountDue ||
		!strings.HasPrefix(sent.HostedInvoiceURL, "https://") {
		t.Fatalf("approved Stripe invoice = %+v", sent)
	}
	replayed := sandboxJSON[api.StatementInvoiceExportResponse](f, http.MethodPost, sendPath, approve, http.StatusOK)
	if replayed.InvoiceID != sent.InvoiceID || replayed.StripeIssuedAt == nil || !replayed.StripeIssuedAt.Equal(*sent.StripeIssuedAt) ||
		replayed.StripeSentAt == nil || !replayed.StripeSentAt.Equal(*sent.StripeSentAt) || replayed.StripeSendPending {
		t.Fatalf("replayed approval changed the send receipt: %+v", replayed)
	}
	if finalized, sends := calls.finalize.Load()-beforeFinalize, calls.send.Load()-beforeSend; finalized != 1 || sends != 1 {
		t.Fatalf("Stripe calls after approval and replay: %d finalizations and %d sends, want one of each", finalized, sends)
	}
	if invoice := readInvoice(); invoice.Status != state.InvoicePaymentOpen || invoice.CollectionMethod != "send_invoice" ||
		invoice.AutoAdvance == nil || *invoice.AutoAdvance || invoice.CustomerEmail != review.RecipientEmail ||
		invoice.AmountPaid != 0 || invoice.AmountRemaining != amountDue ||
		invoice.DueDate == nil || !invoice.DueDate.Equal(review.DueAt) || invoice.HostedURL != sent.HostedInvoiceURL {
		t.Fatalf("Stripe invoice does not match the approved terms: %+v", invoice)
	}

	if code := stripe.call(http.MethodPost, rawPath+"/pay", url.Values{"payment_method": {customer.PaymentMethodID}}, nil); code != http.StatusOK {
		t.Fatalf("pay reviewed Stripe invoice with test card: %d", code)
	}
	deadline := time.Now().Add(90 * time.Second)
	paid := readInvoice()
	for paid.Status == state.InvoicePaymentOpen && time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		paid = readInvoice()
	}
	if paid.Status != state.InvoicePaymentPaid || paid.AmountPaid != amountDue || paid.AmountRemaining != 0 || paid.PaidAt == nil {
		t.Fatalf("Stripe test payment = %+v", paid)
	}
	synced := f.sync(invoicePath)
	if synced.InvoiceID != draft.InvoiceID || synced.Livemode || synced.Currency != "EUR" || synced.PaymentStatus != state.InvoicePaymentPaid ||
		synced.AmountPaidMinor == nil || *synced.AmountPaidMinor != paid.AmountPaid ||
		synced.AmountRemainingMinor == nil || *synced.AmountRemainingMinor != 0 ||
		synced.PaidAt == nil || !synced.PaidAt.Equal(*paid.PaidAt) || synced.StatusSyncedAt == nil {
		t.Fatalf("synced Stripe test payment = %+v", synced)
	}
	progress := sandboxJSON[api.APIConsumerBillingProgressResponse](f, http.MethodGet, consumerPath+"/billing-progress", nil, http.StatusOK)
	invoice := progress.Invoice
	if invoice == nil || invoice.Provider != state.BillingProviderStripe ||
		invoice.Livemode || invoice.PaymentStatus != state.InvoicePaymentPaid ||
		invoice.SentAt == nil || invoice.PaidAt == nil || progress.FirstLivePaidAt != nil {
		t.Fatalf("billing progress after test payment: invoice %+v, first live payment %v", invoice, progress.FirstLivePaidAt)
	}
}

type sandboxStripeIssueCalls struct {
	next     http.RoundTripper
	finalize atomic.Int32
	send     atomic.Int32
}

func (c *sandboxStripeIssueCalls) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/invoices/") {
		switch {
		case strings.HasSuffix(r.URL.Path, "/finalize"):
			c.finalize.Add(1)
		case strings.HasSuffix(r.URL.Path, "/send"):
			c.send.Add(1)
		}
	}
	return c.next.RoundTrip(r)
}

func cleanReviewedSandboxInvoice(stripe sandboxHTTP, client *stripeinvoice.Client, key, id string) {
	t := stripe.t
	t.Helper()
	invoice, err := client.GetInvoice(context.Background(), key, id)
	if err != nil {
		t.Errorf("read Stripe sandbox invoice for cleanup: %v", err)
		return
	}
	if invoice.Deleted {
		return
	}
	if invoice.Livemode == nil || *invoice.Livemode {
		t.Error("cannot confirm test mode for Stripe invoice cleanup")
		return
	}
	path := "/v1/invoices/" + url.PathEscape(id)
	switch invoice.Status {
	case state.InvoicePaymentDraft:
		if code := stripe.call(http.MethodDelete, path, nil, nil); code != http.StatusOK {
			t.Errorf("delete reviewed Stripe draft: %d", code)
		}
	case state.InvoicePaymentOpen, state.InvoicePaymentUncollectible:
		if code := stripe.call(http.MethodPost, path+"/void", url.Values{}, nil); code != http.StatusOK {
			t.Errorf("void reviewed Stripe invoice: %d", code)
		}
	}
}
