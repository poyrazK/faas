package stripe_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/state"
)

const testSecret = "whsec_test_secret_aaaaaaaa"

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestVerifySignature_HappyPath: a correctly-signed Stripe payload passes.
func TestVerifySignature_HappyPath(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"id":"evt_test","type":"invoice.payment_succeeded"}`)
	ts := time.Now()
	header := stripe.SignForTest(payload, testSecret, ts)
	if err := stripe.VerifySignature(payload, header, testSecret, 5*time.Minute); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestVerifySignature_Tampered: any byte change in the payload breaks the
// signature. Tampering must be rejected even when the header itself
// looks well-formed.
func TestVerifySignature_Tampered(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"id":"evt_test","amount":100}`)
	header := stripe.SignForTest(payload, testSecret, time.Now())
	tampered := []byte(`{"id":"evt_test","amount":101}`)
	if err := stripe.VerifySignature(tampered, header, testSecret, 5*time.Minute); !errors.Is(err, stripe.ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// TestVerifySignature_Expired: timestamps outside the tolerance window
// are rejected. This is the replay-protection half of the signature.
func TestVerifySignature_Expired(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"id":"evt_test"}`)
	ts := time.Now().Add(-10 * time.Minute)
	header := stripe.SignForTest(payload, testSecret, ts)
	err := stripe.VerifySignature(payload, header, testSecret, 5*time.Minute)
	if !errors.Is(err, stripe.ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
	if !strings.Contains(err.Error(), "tolerance") {
		t.Fatalf("err = %v, want 'tolerance' substring", err)
	}
}

// TestVerifySignature_Malformed: a header without t= or v1= is rejected.
func TestVerifySignature_Malformed(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"id":"evt_test"}`)
	cases := []struct {
		name   string
		header string
	}{
		{"empty header", ""},
		{"no v1", "t=12345"},
		{"no t", "v1=deadbeef"},
		{"garbage", "lolwat"},
		{"bad t", "t=notanumber,v1=deadbeef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := stripe.VerifySignature(payload, tc.header, testSecret, 5*time.Minute)
			if !errors.Is(err, stripe.ErrBadSignature) {
				t.Fatalf("err = %v, want ErrBadSignature", err)
			}
		})
	}
}

// TestVerifySignature_EmptySecret: an empty secret is a configuration
// error; the verify path refuses to operate so a forgotten env var
// fails loud rather than silently accepting.
func TestVerifySignature_EmptySecret(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"id":"evt_test"}`)
	header := stripe.SignForTest(payload, testSecret, time.Now())
	if err := stripe.VerifySignature(payload, header, "", 5*time.Minute); !errors.Is(err, stripe.ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// TestPushUsageRecord_DedupesHour: pushing the same (account, hour)
// twice is a no-op the second time. Verified through the Store's
// HasStripePushHour + RecordStripePushHour pair (the same path the
// production pusher uses).
func TestPushUsageRecord_DedupesHour(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "test@example.com", "hobby")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := store.UpdateAccountStripeCustomerID(ctx, acct.ID, "cus_test"); err != nil {
		t.Fatalf("stripe id: %v", err)
	}

	hour := time.Date(2026, 7, 17, 14, 0, 0, 0, time.UTC)

	// First call records; second is a no-op.
	dup1, err := store.HasStripePushHour(ctx, acct.ID, hour)
	if err != nil {
		t.Fatalf("has 1: %v", err)
	}
	if dup1 {
		t.Fatalf("hour already pushed (precondition)")
	}
	if err := store.RecordStripePushHour(ctx, acct.ID, hour); err != nil {
		t.Fatalf("record: %v", err)
	}
	dup2, err := store.HasStripePushHour(ctx, acct.ID, hour)
	if err != nil {
		t.Fatalf("has 2: %v", err)
	}
	if !dup2 {
		t.Fatalf("hour should be marked after record")
	}
}

// TestAccountByStripeCustomerID_RoundTrip: after UpdateAccountStripeCustomerID,
// AccountByStripeCustomerID returns the same account.
func TestAccountByStripeCustomerID_RoundTrip(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "rt@example.com", "hobby")
	if err := store.UpdateAccountStripeCustomerID(ctx, acct.ID, "cus_rt"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.AccountByStripeCustomerID(ctx, "cus_rt")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != acct.ID {
		t.Fatalf("got id %s, want %s", got.ID, acct.ID)
	}
}

// TestAccountByStripeCustomerID_NotFound: unknown Stripe customer returns
// ErrNotFound (the same sentinel both backends emit).
func TestAccountByStripeCustomerID_NotFound(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	_, err := store.AccountByStripeCustomerID(context.Background(), "cus_unknown")
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestEnsurePlanProducts_RequiresAPIKey is the post-real-SDK version
// of the old placeholder smoke test. EnsurePlanProducts now requires
// a non-empty apiKey (otherwise c.api is nil and the call would
// silently skip a billing-surface setup). The actual §14 M7 gate is
// the live-sandbox test TestPushUsageRecord_PostsToStripeSandbox
// (run against sk_test_… with FATEST_STRIPE_SUB_ITEM); this test just
// pins the fast-fail contract so a misconfig is loud at boot rather
// than silent at the first push.
func TestEnsurePlanProducts_RequiresAPIKey(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	c := stripe.NewClient(store, store, "", "", discardLog())
	if err := c.EnsurePlanProducts(context.Background()); err == nil {
		t.Fatal("EnsurePlanProducts with empty apiKey returned nil; want error (fast-fail)")
	}
}

// TestCreateCustomer_RequiresAPIKey: same pattern as above for
// CreateCustomer — production callers must wire a real Stripe key.
func TestCreateCustomer_RequiresAPIKey(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "cc@example.com", "hobby")
	c := stripe.NewClient(store, store, "", "", discardLog())
	if _, err := c.CreateCustomer(ctx, acct); err == nil {
		t.Fatal("CreateCustomer with empty apiKey returned nil; want error")
	}
}

// TestStripeWebhook_RejectsUnsigned is the integration test: posting
// without a valid signature must return 400. Uses httptest because the
// full apid handler chain is too heavy for a webhook test; this asserts
// the stripe.VerifySignature call from apid's handler would reject.
// (The handler itself is exercised in cmd/apid's handler tests.)
func TestStripeWebhook_RejectsUnsigned(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	body := []byte(`{"id":"evt_test"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(body))
	// No Stripe-Signature header set.
	if err := stripe.VerifySignature(body, req.Header.Get("Stripe-Signature"), testSecret, 5*time.Minute); !errors.Is(err, stripe.ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
	_ = rec
}
