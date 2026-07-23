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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

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

// signPaddleBody computes the canonical `ts=N;h1=H` header for the
// test body using the project's HMAC scheme (ts:body, separator ":"
// — not ".", as Stripe does). Tests construct signatures via this
// helper rather than calling the package's verifyPaddleSignature
// itself; that's the surface VerifyWebhook uses.
func signPaddleBody(secret string, body []byte, when time.Time) string {
	ts := strconvFormatInt(when.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + ":"))
	mac.Write(body)
	return "ts=" + ts + ";h1=" + hex.EncodeToString(mac.Sum(nil))
}

// strconvFormatInt is a tiny helper to keep the test file from
// dragging in `strconv` at the top (we want zero non-stdlib imports
// for the cheapest build).
func strconvFormatInt(v int64, base int) string {
	return fmt.Sprintf("%d", v)
}

func TestVerifyPaddleSignature_RoundTrip(t *testing.T) {
	t.Parallel()

	body := []byte(`{"event_id":"evt_unit_ok","event_type":"transaction.paid"}`)
	when := time.Now()
	header := signPaddleBody(testWebhookKey, body, when)

	p := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
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

	p := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	if _, err := p.VerifyWebhook(body, map[string]string{"paddle-signature": header}, time.Minute); err != nil {
		t.Fatalf("VerifyWebhook lower-case header: %v", err)
	}
}

func TestVerifyPaddleSignature_RejectsBadSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{"event_id":"evt_bad"}`)
	when := time.Now()
	wrong := signPaddleBody("not-the-real-secret-0123456789abcdef", body, when)

	p := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	_, err := p.VerifyWebhook(body, map[string]string{"Paddle-Signature": wrong}, time.Minute)
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

	p := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	_, err := p.VerifyWebhook(body, map[string]string{"Paddle-Signature": header}, 0) // 0 → default 5m tolerance
	if err == nil {
		t.Fatal("VerifyWebhook accepted a stale signature")
	}
	if !errors.Is(err, billing.ErrBadSignature) {
		t.Errorf("err = %v, want errors.Is ErrBadSignature", err)
	}
}

func TestVerifyPaddleSignature_RejectsEmptySecret(t *testing.T) {
	t.Parallel()

	p := paddle.NewProvider(testAPIKey, "", true, discardLog())
	_, err := p.VerifyWebhook([]byte(`{}`), map[string]string{"Paddle-Signature": "ts=0;h1=00"}, 0)
	if err == nil {
		t.Fatal("VerifyWebhook accepted empty webhook secret")
	}
	if !errors.Is(err, billing.ErrBadSignature) {
		t.Errorf("err = %v, want errors.Is ErrBadSignature", err)
	}
}

func TestVerifyPaddleSignature_RejectsMissingHeader(t *testing.T) {
	t.Parallel()

	p := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	_, err := p.VerifyWebhook([]byte(`{}`), map[string]string{}, 0)
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
	p := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
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
		{"unknown", "transaction.completed", billing.EventUnknown},
		{"empty", "", billing.EventUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Appendf(nil, `{"event_id":"evt_%s","event_type":%q,"data":{"customer_id":"ctm_xyz","subscription_id":"sub_abc","items":[{"price":{"id":"pri_test"}}]}}`, tc.name, tc.eventType)
			p := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
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
			if tc.want != billing.EventUnknown && tc.name != "unknown" {
				if ev.PlanID != "pri_test" {
					t.Errorf("PlanID = %q, want pri_test", ev.PlanID)
				}
			}
		})
	}
}

func TestParsePaddleEvent_RejectsMalformedBody(t *testing.T) {
	t.Parallel()

	p := paddle.NewProvider(testAPIKey, testWebhookKey, true, discardLog())
	// Build a real signature over a body that's NOT valid JSON, so
	// the HMAC verifier succeeds but parsePaddleEvent fails.
	bad := []byte(`{not even json`)
	when := time.Now()
	header := signPaddleBody(testWebhookKey, bad, when)
	_, err := p.VerifyWebhook(bad, map[string]string{"Paddle-Signature": header}, time.Minute)
	if err == nil {
		t.Fatal("VerifyWebhook accepted malformed JSON body")
	}
}

func TestRedactAPIKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{"pdl_abcdefghijklmnop", "pdl_" + strings.Repeat("*", 16)},
		{"abcd", "***"},
		{"abc", "***"},
		{"", "***"},
	}
	for _, tc := range cases {
		got := paddle.RedactAPIKeyForTest(tc.in)
		if got != tc.want {
			t.Errorf("redact(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
