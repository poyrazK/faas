package paddle_test

// Unit tests for pkg/billing/paddle that don't require a live
// Paddle API key:
//
//   - HMAC round-trip (verifyPaddleSignature) — pinned against the
//     canonical-string format `ts:body` with HMAC-SHA256.
//   - VerifyWebhook header tolerance + missing-secret + bad-signature
//     paths.
//   - parsePaddleEvent / mapPaddleEventType coverage of all six
//     normalized EventType values + the unmapped default.
//
// Live sandbox coverage lives in sandbox_test.go (gated by
// PADDLE_API_KEY). Provider-pluggable surface conformance (the
// _Provider var) is pinned by package-level compilation.

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
)

const (
	testAPIKey     = "pdl_test_dummy_unit"
	testWebhookKey = "whk_unit_test_secret_0123456789abcdef"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// signPaddleBody builds a valid Paddle-Signature header for tests
// by delegating to the package's SignForTestForTest seam (parity
// with pkg/billing/stripe/webhook.go::SignForTest). Wrapping
// keeps the call sites short.
func signPaddleBody(secret string, body []byte, when time.Time) string {
	return paddle.SignForTestForTest(body, secret, when)
}

func TestVerifyPaddleSignature_RoundTrip(t *testing.T) {
	t.Parallel()

	body := []byte(`{"event_id":"evt_unit_ok","event_type":"transaction.paid"}`)
	when := time.Now()
	header := signPaddleBody(testWebhookKey, body, when)

	p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if err != nil {
		t.Fatalf("paddle.NewProvider: %v", err)
	}
	ev, err := p.VerifyWebhook(body, map[string]string{"Paddle-Signature": header}, time.Minute)
	if err != nil {
		t.Fatalf("VerifyWebhook happy-path: %v", err)
	}
	if ev.Type != billing.EventPaymentSucceeded {
		t.Errorf("Type = %v, want EventPaymentSucceeded", ev.Type)
	}
}

func TestVerifyPaddleSignature_LowercaseHeaderAccepted(t *testing.T) {
	t.Parallel()

	body := []byte(`{"event_id":"evt_lc"}`)
	when := time.Now()
	header := signPaddleBody(testWebhookKey, body, when)

	p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if err != nil {
		t.Fatalf("paddle.NewProvider: %v", err)
	}
	if _, err := p.VerifyWebhook(body, map[string]string{"paddle-signature": header}, time.Minute); err != nil {
		t.Fatalf("VerifyWebhook lower-case header: %v", err)
	}
}

func TestVerifyPaddleSignature_RejectsBadSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{"event_id":"evt_bad"}`)
	when := time.Now()
	wrong := signPaddleBody("not-the-real-secret-0123456789abcdef", body, when)

	p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if err != nil {
		t.Fatalf("paddle.NewProvider: %v", err)
	}
	_, err = p.VerifyWebhook(body, map[string]string{"Paddle-Signature": wrong}, time.Minute)
	if err == nil {
		t.Fatal("VerifyWebhook accepted wrong-secret signature")
	}
	if !errors.Is(err, billing.ErrBadSignature) {
		t.Errorf("err = %v, want errors.Is ErrBadSignature", err)
	}
}

func TestVerifyPaddleSignature_RejectsClockSkew(t *testing.T) {
	t.Parallel()

	body := []byte(`{}`)
	stale := time.Now().Add(-10 * time.Minute) // outside 5-min default tolerance
	header := signPaddleBody(testWebhookKey, body, stale)

	p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if err != nil {
		t.Fatalf("paddle.NewProvider: %v", err)
	}
	_, err = p.VerifyWebhook(body, map[string]string{"Paddle-Signature": header}, 0) // 0 → default 5m tolerance
	if err == nil {
		t.Fatal("VerifyWebhook accepted a stale signature")
	}
	if !errors.Is(err, billing.ErrBadSignature) {
		t.Errorf("err = %v, want errors.Is ErrBadSignature", err)
	}
}

func TestVerifyPaddleSignature_RejectsEmptySecret(t *testing.T) {
	t.Parallel()

	p, err := paddle.NewProvider(testAPIKey, "", true, discardLog())
	if err != nil {
		t.Fatalf("paddle.NewProvider: %v", err)
	}
	_, err = p.VerifyWebhook([]byte(`{}`), map[string]string{"Paddle-Signature": "ts=0;h1=00"}, 0)
	if err == nil {
		t.Fatal("VerifyWebhook accepted empty webhook secret")
	}
	if !errors.Is(err, billing.ErrBadSignature) {
		t.Errorf("err = %v, want errors.Is ErrBadSignature", err)
	}
}

func TestVerifyPaddleSignature_RejectsMissingHeader(t *testing.T) {
	t.Parallel()

	p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if err != nil {
		t.Fatalf("paddle.NewProvider: %v", err)
	}
	_, err = p.VerifyWebhook([]byte(`{}`), map[string]string{}, 0)
	if err == nil {
		t.Fatal("VerifyWebhook accepted empty headers")
	}
	if !errors.Is(err, billing.ErrBadSignature) {
		t.Errorf("err = %v, want errors.Is ErrBadSignature", err)
	}
}

func TestVerifyPaddleSignature_RejectsMalformedHeader(t *testing.T) {
	t.Parallel()

	cases := []string{
		"ts=abc;h1=deadbeef",                           // bad ts
		"ts=1234567890",                                // missing h1
		"ts=1234567890;h1=nothexhere",                  // bad h1 (not 64 hex)
		"v1=foo",                                       // wrong scheme
		"ts=1234567890;h1=00;sneaky=injection",         // extra fields
		"ts=1234567890 ;h1=" + strings.Repeat("0", 64), // whitespace within fields
	}
	p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if err != nil {
		t.Fatalf("paddle.NewProvider: %v", err)
	}
	for _, header := range cases {
		t.Run(header, func(t *testing.T) {
			_, err := p.VerifyWebhook([]byte(`{}`), map[string]string{"Paddle-Signature": header}, 0)
			if err == nil {
				t.Fatalf("VerifyWebhook accepted malformed header %q", header)
			}
			if !errors.Is(err, billing.ErrBadSignature) {
				t.Errorf("err = %v, want errors.Is ErrBadSignature", err)
			}
		})
	}
}

func TestParsePaddleEvent_MapsAllSubscriptionTypes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		eventType string
		want      billing.EventType
	}{
		{"subscription_created", "subscription.created", billing.EventSubscriptionCreated},
		{"subscription_updated", "subscription.updated", billing.EventSubscriptionUpdated},
		{"subscription_canceled", "subscription.canceled", billing.EventSubscriptionCanceled},
		{"subscription_past_due", "subscription.past_due", billing.EventSubscriptionPastDue},
		{"transaction_paid", "transaction.paid", billing.EventPaymentSucceeded},
		{"transaction_payment_failed", "transaction.payment_failed", billing.EventPaymentFailed},
		{"transaction_completed", "transaction.completed", billing.EventPaymentSucceeded},
		{"empty", "", billing.EventUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Appendf(nil, `{"event_id":"evt_%s","event_type":%q,"data":{"customer_id":"ctm_xyz","subscription_id":"sub_abc","items":[{"price":{"id":"pri_test"}}]}}`, tc.name, tc.eventType)
			p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
			if err != nil {
				t.Fatalf("paddle.NewProvider: %v", err)
			}
			pNow := time.Now()
			header := signPaddleBody(testWebhookKey, body, pNow)
			ev, err := p.VerifyWebhook(body, map[string]string{"Paddle-Signature": header}, time.Minute)
			if err != nil {
				t.Fatalf("VerifyWebhook: %v", err)
			}
			if ev.Type != tc.want {
				t.Errorf("Type = %v, want %v", ev.Type, tc.want)
			}
			if ev.CustomerID != "ctm_xyz" {
				t.Errorf("CustomerID = %q, want ctm_xyz", ev.CustomerID)
			}
			if ev.SubscriptionID != "sub_abc" {
				t.Errorf("SubscriptionID = %q, want sub_abc", ev.SubscriptionID)
			}
			// Issue #294: parsePaddleEvent must populate EventID so
			// apid's webhook replay dedupe has a stable key.
			wantEventID := fmt.Sprintf("evt_%s", tc.name)
			if ev.EventID != wantEventID {
				t.Errorf("EventID = %q, want %q", ev.EventID, wantEventID)
			}
			if tc.want != billing.EventUnknown && tc.name != "unknown" {
				if ev.PlanID != "pri_test" {
					t.Errorf("PlanID = %q, want pri_test", ev.PlanID)
				}
			}
		})
	}
}

func TestVerifyWebhookUsesSubscriptionIDFromEventIDField(t *testing.T) {
	p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	body := []byte(`{"event_id":"evt_subscription","event_type":"subscription.created","data":{"id":"sub-actual","customer_id":"ctm-1","custom_data":{"plan":"pro"},"items":[{"price":{"id":"pri-hobby"}}]}}`)
	ev, err := p.VerifyWebhook(body, map[string]string{"Paddle-Signature": signPaddleBody(testWebhookKey, body, when)}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ev.SubscriptionID != "sub-actual" {
		t.Fatalf("subscription id = %q, want sub-actual", ev.SubscriptionID)
	}
	if ev.PlanID != string(api.PlanPro) {
		t.Fatalf("plan id = %q, want canonical %q", ev.PlanID, api.PlanPro)
	}
}

func TestParsePaddleEvent_RejectsMalformedBody(t *testing.T) {
	t.Parallel()

	p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if err != nil {
		t.Fatalf("paddle.NewProvider: %v", err)
	}
	// Build a real signature over a body that's NOT valid JSON, so
	// the HMAC verifier succeeds but parsePaddleEvent fails.
	bad := []byte(`{not even json`)
	when := time.Now()
	header := signPaddleBody(testWebhookKey, bad, when)
	_, err = p.VerifyWebhook(bad, map[string]string{"Paddle-Signature": header}, time.Minute)
	if err == nil {
		t.Fatal("VerifyWebhook accepted malformed JSON body")
	}
}

func TestVerifyWebhookProjectsCompletedTransactionInvoice(t *testing.T) {
	p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	body := []byte(`{"event_id":"evt_completed","event_type":"transaction.completed","data":{"id":"txn-1","invoice_id":"inv-1","customer_id":"ctm-1","subscription_id":"sub-1","status":"completed","invoice_number":"INV-1","currency_code":"EUR","billing_period":{"starts_at":"2026-08-01T00:00:00Z","ends_at":"2026-09-01T00:00:00Z"},"details":{"totals":{"subtotal":"900","tax":"100","total":"1000"}}}}`)
	ev, err := p.VerifyWebhook(body, map[string]string{"Paddle-Signature": signPaddleBody(testWebhookKey, body, when)}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != billing.EventPaymentSucceeded {
		t.Fatalf("event type = %v, want payment_succeeded", ev.Type)
	}
	if ev.Invoice == nil {
		t.Fatal("completed transaction did not produce an invoice projection")
	}
	if ev.Invoice.ProviderInvoiceID != "inv-1" || ev.Invoice.Number != "INV-1" || ev.Invoice.Status != "paid" || ev.Invoice.SubtotalCents != 900 || ev.Invoice.TaxCents != 100 || ev.Invoice.TotalCents != 1000 || ev.Invoice.AmountPaidCents != 1000 || !ev.Invoice.PDFAvailable {
		t.Fatalf("invoice projection = %+v", ev.Invoice)
	}
}

func TestVerifyWebhookMapsRefundAdjustment(t *testing.T) {
	p, err := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	body := []byte(`{"event_id":"evt_refund","event_type":"adjustment.updated","data":{"id":"adj-1","action":"refund","customer_id":"ctm-1","transaction_id":"txn-1","currency_code":"EUR","totals":{"total":"500"}}}`)
	ev, err := p.VerifyWebhook(body, map[string]string{"Paddle-Signature": signPaddleBody(testWebhookKey, body, when)}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != billing.EventRefundProcessed || ev.ProviderRefundID != "adj-1" || ev.ChargeID != "txn-1" || ev.AmountCents != 500 || ev.Currency != "EUR" {
		t.Fatalf("refund event = %+v", ev)
	}
}
