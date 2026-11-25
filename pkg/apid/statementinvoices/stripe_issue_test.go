package statementinvoices

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

const issueInvoiceID = "in_reviewed"

type issueHost struct {
	Host
	store     *state.MemStore
	accountID string
}

func (h issueHost) Store() state.Store { return h.store }

func (h issueHost) ConsumerFeatureAllowed(http.ResponseWriter, state.Account) bool { return true }

func (h issueHost) OpenSecret(secret []byte) (string, []byte, error) {
	return keyNamespace(state.BillingProviderStripe, h.accountID), secret, nil
}

func (issueHost) Audit(context.Context, string, string, map[string]any) {}

type issueStripeFixture struct {
	mu                     sync.Mutex
	doc                    map[string]any
	changedAtFinalization  bool
	loseFinalize, loseSend bool
	finalizations, sends   int
	requests, writes       int
	finalizeKey, sendKey   string
	now                    time.Time
	sendEvents             []map[string]any
}

func (f *issueStripeFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if r.Method == http.MethodPost {
		f.writes++
	}
	const path = "/v1/invoices/" + issueInvoiceID
	key := r.Header.Get("Idempotency-Key")
	lost := false
	switch {
	case r.Method == http.MethodGet && r.URL.Path == path:
	case r.Method == http.MethodGet && r.URL.Path == "/v1/events" && r.URL.Query().Get("type") == "invoice.sent":
		_ = json.NewEncoder(w).Encode(map[string]any{"data": f.sendEvents, "has_more": false})
		return
	case r.Method == http.MethodPost && r.URL.Path == path && key != "" &&
		r.FormValue("collection_method") == "send_invoice" && r.FormValue("auto_advance") == "false" && f.doc["status"] == "draft":
		due, err := strconv.ParseInt(r.FormValue("due_date"), 10, 64)
		if err != nil {
			http.Error(w, "invalid due date", http.StatusBadRequest)
			return
		}
		f.doc["due_date"], f.doc["collection_method"] = due, "send_invoice"
	case r.Method == http.MethodPost && r.URL.Path == path+"/finalize" && key != "" &&
		r.FormValue("auto_advance") == "false" && f.doc["collection_method"] == "send_invoice" && (f.doc["status"] == "draft" || key == f.finalizeKey):
		if f.finalizeKey == "" {
			f.finalizations++
			f.finalizeKey = key
			f.doc["status"], f.doc["hosted_invoice_url"] = "open", "https://invoice.stripe.example/i/reviewed"
			f.doc["status_transitions"] = map[string]int64{"finalized_at": f.now.Unix()}
			if f.changedAtFinalization {
				f.doc["total"], f.doc["amount_due"], f.doc["amount_remaining"] = 1900, 1900, 1900
			}
			lost = f.loseFinalize
		}
	case r.Method == http.MethodPost && r.URL.Path == path+"/send" && key != "" &&
		f.doc["status"] == "open" && f.doc["collection_method"] == "send_invoice" && (f.sendKey == "" || f.sendKey == key):
		if f.sendKey == "" {
			f.sends++
			f.sendKey, lost = key, f.loseSend
		}
	default:
		http.Error(w, "unexpected Stripe operation", http.StatusBadRequest)
		return
	}
	if lost {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
			return
		}
		http.Error(w, "lost provider response", http.StatusBadGateway)
		return
	}
	_ = json.NewEncoder(w).Encode(f.doc)
}

type issueFixture struct {
	service   *Service
	store     *state.MemStore
	stripe    *issueStripeFixture
	account   state.Account
	statement string
	now       time.Time
}

func newIssueFixture(t *testing.T) *issueFixture {
	t.Helper()
	f := &issueFixture{store: state.NewMemStore(), account: state.Account{ID: uuid.NewString()},
		statement: uuid.NewString(), now: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	f.stripe = &issueStripeFixture{now: f.now, doc: map[string]any{
		"id": issueInvoiceID, "status": "draft", "customer": "cus_REVIEWED", "customer_email": "billing@example.test",
		"currency": "usd", "livemode": false, "auto_advance": false, "amount_paid": 0,
		"collection_method": "charge_automatically", "total": 1500, "amount_due": 1500, "amount_remaining": 1500,
		"metadata": map[string]string{"gregale_statement_id": f.statement, "gregale_source": state.StatementSourceAppConsumer},
	}}
	server := httptest.NewServer(f.stripe)
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	t.Cleanup(transport.CloseIdleConnections)
	f.service = &Service{Host: issueHost{store: f.store, accountID: f.account.ID}, Now: func() time.Time { return f.now },
		Stripe: &stripeinvoice.Client{BaseURL: server.URL, HTTP: &http.Client{Transport: transport}}}
	ctx := context.Background()
	_, err := f.store.PutBillingConnection(ctx, state.BillingConnection{AccountID: f.account.ID, Provider: "stripe",
		KeyLast4: "test", SealedSecret: []byte("rk_test_fixture")})
	if err != nil {
		t.Fatal(err)
	}
	export, _, err := f.store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: f.account.ID,
		SourceKind: state.StatementSourceAppConsumer, StatementID: f.statement, Provider: "stripe",
		Customer: "cus_REVIEWED", Currency: "USD", AmountMinor: 1200})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetStatementInvoiceExportInvoice(ctx, f.account.ID, export.ID, issueInvoiceID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CompleteStatementInvoiceExport(ctx, f.account.ID, export.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *issueFixture) request(method, token string, account state.Account) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/account/statement-invoices/"+f.statement+"/send",
		strings.NewReader(`{"review_token":"`+token+`"}`))
	r.SetPathValue("statement_id", f.statement)
	w := httptest.NewRecorder()
	if method == http.MethodGet {
		f.service.ReviewStripeInvoiceSend(w, r, account)
	} else {
		f.service.SendReviewedStripeInvoice(w, r, account)
	}
	return w
}

func (f *issueFixture) review(t *testing.T) api.StripeInvoiceSendReview {
	t.Helper()
	w := f.request(http.MethodGet, "", f.account)
	var review api.StripeInvoiceSendReview
	if err := json.Unmarshal(w.Body.Bytes(), &review); err != nil || w.Code != http.StatusOK || !validStripeSendToken(review.ReviewToken) {
		t.Fatalf("review status=%d body=%s", w.Code, w.Body)
	}
	return review
}

func TestReviewedStripeInvoiceResumesFinalizationAndSend(t *testing.T) {
	f := newIssueFixture(t)
	f.stripe.loseFinalize, f.stripe.loseSend = true, true
	review := f.review(t)
	if review.Livemode || review.InvoiceID != issueInvoiceID || review.AmountDueMinor != 1500 ||
		review.RecipientEmail != "billing@example.test" || review.DueAt.Format(time.DateOnly) != "2026-12-22" {
		t.Fatalf("review = %+v", review)
	}
	for range 2 {
		w := f.request(http.MethodPost, review.ReviewToken, f.account)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("lost Stripe response: %d %s", w.Code, w.Body)
		}
		if retry := f.review(t); retry.ReviewToken != review.ReviewToken {
			t.Fatalf("recovery review changed: %+v", retry)
		}
	}
	for range 2 {
		w := f.request(http.MethodPost, review.ReviewToken, f.account)
		var out api.StatementInvoiceExportResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK ||
			out.InvoiceID != issueInvoiceID || out.StripeIssuedAt == nil || out.StripeSentAt == nil || out.StripeSendPending ||
			out.PaymentStatus != "open" || out.AmountRemainingMinor == nil || *out.AmountRemainingMinor != 1500 || out.HostedInvoiceURL == "" {
			t.Fatalf("send status=%d body=%s", w.Code, w.Body)
		}
	}
	f.stripe.mu.Lock()
	finalizations, sends, writes := f.stripe.finalizations, f.stripe.sends, f.stripe.writes
	f.now = f.now.Add(2 * time.Hour)
	f.stripe.doc["status"], f.stripe.doc["amount_paid"], f.stripe.doc["amount_remaining"] = "paid", 1500, 0
	f.stripe.doc["status_transitions"] = map[string]int64{"finalized_at": f.stripe.now.Unix(), "paid_at": f.now.Unix()}
	f.stripe.mu.Unlock()
	if finalizations != 1 || sends != 1 || writes != 5 {
		t.Fatalf("finalizations=%d unique sends=%d write requests=%d", finalizations, sends, writes)
	}
	ctx := context.Background()
	if err := f.service.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	paid, err := f.store.GetStatementInvoiceExport(ctx, f.account.ID, state.StatementSourceAppConsumer, f.statement)
	if err != nil || paid.ProviderInvoiceID != issueInvoiceID || paid.PaymentStatus != state.InvoicePaymentPaid ||
		paid.PaidAt == nil || paid.AmountPaidMinor == nil || *paid.AmountPaidMinor != 1500 ||
		paid.StripeIssue == nil || paid.StripeIssue.SentAt == nil {
		t.Fatalf("payment sync = %+v, err = %v", paid, err)
	}
}

func TestReviewedStripeInvoiceRejectsChangedTerms(t *testing.T) {
	f := newIssueFixture(t)
	review := f.review(t)
	f.stripe.mu.Lock()
	f.stripe.doc["customer_email"] = "changed@example.test"
	f.stripe.changedAtFinalization = true
	f.stripe.mu.Unlock()
	w := f.request(http.MethodPost, review.ReviewToken, f.account)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale review: %d %s", w.Code, w.Body)
	}
	unrelated := state.Account{ID: uuid.NewString()}
	if w := f.request(http.MethodPost, review.ReviewToken, unrelated); w.Code != http.StatusNotFound {
		t.Fatalf("foreign account: %d %s", w.Code, w.Body)
	}
	updated := f.review(t)
	if updated.RecipientEmail != "changed@example.test" || updated.ReviewToken == review.ReviewToken {
		t.Fatalf("updated review = %+v", updated)
	}
	w = f.request(http.MethodPost, updated.ReviewToken, f.account)
	if w.Code != http.StatusConflict {
		t.Fatalf("changed amount at finalization: %d %s", w.Code, w.Body)
	}
	w = f.request(http.MethodPost, updated.ReviewToken, f.account)
	if w.Code != http.StatusConflict {
		t.Fatalf("retry after changed terms: %d %s", w.Code, w.Body)
	}
	f.stripe.mu.Lock()
	defer f.stripe.mu.Unlock()
	if f.stripe.finalizations != 1 || f.stripe.sends != 0 || f.stripe.writes != 2 {
		t.Fatalf("finalizations=%d sends=%d writes=%d", f.stripe.finalizations, f.stripe.sends, f.stripe.writes)
	}
}

func TestReviewedStripeInvoiceExpiresAnUnknownDelivery(t *testing.T) {
	f := newIssueFixture(t)
	f.stripe.loseSend = true
	review := f.review(t)
	if w := f.request(http.MethodPost, review.ReviewToken, f.account); w.Code != http.StatusBadGateway {
		t.Fatalf("lost send response: %d %s", w.Code, w.Body)
	}
	f.now = f.now.Add(24 * time.Hour)
	w := f.request(http.MethodPost, review.ReviewToken, f.account)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "retry window expired") {
		t.Fatalf("late retry: %d %s", w.Code, w.Body)
	}
	f.stripe.mu.Lock()
	defer f.stripe.mu.Unlock()
	if f.stripe.sends != 1 || f.stripe.writes != 3 {
		t.Fatalf("late retry reached Stripe: %d sends, %d writes", f.stripe.sends, f.stripe.writes)
	}
}

func TestReviewedStripeInvoiceRecoversDeliveryAfterPayment(t *testing.T) {
	for _, recovery := range []string{"retry", "payment refresh"} {
		t.Run(recovery, func(t *testing.T) {
			f := newIssueFixture(t)
			f.stripe.loseSend = true
			review := f.review(t)
			if w := f.request(http.MethodPost, review.ReviewToken, f.account); w.Code != http.StatusBadGateway {
				t.Fatalf("lost send response: %d %s", w.Code, w.Body)
			}
			sentAt := f.now.Add(time.Minute)
			f.now = f.now.Add(24 * time.Hour)
			f.stripe.mu.Lock()
			f.stripe.doc["status"], f.stripe.doc["amount_paid"], f.stripe.doc["amount_remaining"] = "paid", 1500, 0
			f.stripe.doc["status_transitions"] = map[string]int64{"finalized_at": f.stripe.now.Unix(), "paid_at": f.now.Unix()}
			sendEvent := func(id, invoiceID, key string, live bool) map[string]any {
				return map[string]any{"id": id, "type": "invoice.sent", "created": sentAt.Unix(), "livemode": live,
					"data":    map[string]any{"object": map[string]string{"id": invoiceID}},
					"request": map[string]string{"idempotency_key": key}}
			}
			f.stripe.sendEvents = []map[string]any{
				sendEvent("evt_OtherInvoice", "in_other", f.stripe.sendKey, false),
				sendEvent("evt_OtherSend", issueInvoiceID, "another-send", false),
				sendEvent("evt_OtherMode", issueInvoiceID, f.stripe.sendKey, true),
			}
			f.stripe.mu.Unlock()

			if w := f.request(http.MethodPost, review.ReviewToken, f.account); w.Code != http.StatusConflict {
				t.Fatalf("accepted payment without a matching send receipt: %d %s", w.Code, w.Body)
			}
			f.stripe.mu.Lock()
			f.stripe.sendEvents = append(f.stripe.sendEvents, sendEvent("evt_Reviewed", issueInvoiceID, f.stripe.sendKey, false))
			f.stripe.mu.Unlock()
			if recovery == "payment refresh" {
				r := httptest.NewRequest(http.MethodPost, "/v1/account/statement-invoices/"+f.statement+"/refresh", nil)
				r.SetPathValue("statement_id", f.statement)
				w := httptest.NewRecorder()
				f.service.RefreshStripePayment(w, r, f.account)
				var out api.StatementInvoiceExportResponse
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK ||
					out.StripeSentAt == nil || !out.StripeSentAt.Equal(sentAt) {
					t.Fatalf("refresh status=%d body=%s", w.Code, w.Body)
				}
			}
			w := f.request(http.MethodPost, review.ReviewToken, f.account)
			var out api.StatementInvoiceExportResponse
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK ||
				out.PaymentStatus != "paid" || out.StripeSendPending || out.StripeSentAt == nil || !out.StripeSentAt.Equal(sentAt) {
				t.Fatalf("recovery status=%d body=%s", w.Code, w.Body)
			}
			f.stripe.mu.Lock()
			defer f.stripe.mu.Unlock()
			if f.stripe.sends != 1 || f.stripe.writes != 3 {
				t.Fatalf("recovery caused another provider write: %d sends, %d writes", f.stripe.sends, f.stripe.writes)
			}
		})
	}
}
