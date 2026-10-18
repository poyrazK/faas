//go:build !no_pg

package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

const paymentWebhookSecret = "polar_whs_private-payment-failed-test-secret"

type paymentInlineRecorder struct{ calls atomic.Int32 }

func (r *paymentInlineRecorder) Send(context.Context, apidserver.Message) error {
	r.calls.Add(1)
	return nil
}

func paymentFlags(t *testing.T, enabled bool) map[string]string {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "payment-intent.age")
	if err := os.WriteFile(path, []byte(identity.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1",
		"FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "billing", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-payment-outbox-test-credential-32-bytes", mailoutbox.VerificationIdentityEnv: path}
	if enabled {
		flags[internalmail.PaymentFailedOutboxEnableEnv] = "1"
	}
	return flags
}

func paymentProvider(t *testing.T, name string) billing.Provider {
	t.Helper()
	switch name {
	case "paddle":
		p, err := paddle.NewProvider("private-offline-provider-test-token", paymentWebhookSecret, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	case "polar":
		p, err := polar.NewProvider(polar.Config{APIKey: "private-offline-provider-test-token", WebhookSecret: paymentWebhookSecret, Sandbox: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	return nil // Stripe's signed ingress retains its no-SDK compatibility path.
}

func paymentWebhook(t *testing.T, h http.Handler, provider, customer, id string, paid, validSignature bool) *httptest.ResponseRecorder {
	t.Helper()
	data := map[string]any{"customer_id": customer}
	eventType := "transaction.payment_failed"
	if paid {
		eventType = "transaction.completed"
	}
	payload := map[string]any{"event_id": id, "event_type": eventType, "data": data}
	if provider == "stripe" {
		eventType = "invoice.payment_failed"
		if paid {
			eventType = "invoice.payment_succeeded"
		}
		payload = map[string]any{"id": id, "type": eventType, "data": map[string]any{"object": map[string]any{"customer": customer}}}
	}
	if provider == "polar" {
		eventType = "subscription.past_due"
		if paid {
			eventType = "subscription.active"
		}
		payload = map[string]any{"type": eventType, "data": data}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/"+provider, bytes.NewReader(body))
	when := time.Now()
	secret := paymentWebhookSecret
	if !validSignature {
		secret = "wrong-payment-webhook-secret"
	}
	switch provider {
	case "stripe":
		r.Header.Set("Stripe-Signature", stripe.SignForTest(body, secret, when))
	case "paddle":
		r.Header.Set("Paddle-Signature", paddle.SignForTestForTest(body, secret, when))
	case "polar":
		r.Header.Set("webhook-id", id)
		r.Header.Set("webhook-timestamp", strconv.FormatInt(when.Unix(), 10))
		r.Header.Set("webhook-signature", polar.SignForTest(body, secret, id, when))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPaymentFailedOutboxSignedWebhooksAndRetryBoundaries(t *testing.T) {
	for _, provider := range []string{"stripe", "paddle", "polar"} {
		for _, mode := range []string{"off", "atomic", "intent_failure", "status_failure", "receipt_failure", "late_enable", "bad_signature", "missing_id"} {
			if provider == "polar" && mode == "missing_id" {
				continue // Standard Webhooks rejects a missing ID during signature verification.
			}
			t.Run(provider+"/"+mode, func(t *testing.T) {
				e := apidserver.SetupPGHandler(t, api.PlanPro)
				s, store, pool := *e.S(), *e.Store(), *e.Pool()
				s.WithBillingProvider(paymentProvider(t, provider))
				*s.StripeWebhookSecret() = paymentWebhookSecret
				id, customer := e.Acct().ID, "customer-"+uuid.NewString()
				if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: id, Provider: provider, CustomerID: customer}); err != nil {
					t.Fatal(err)
				}
				flags := paymentFlags(t, mode != "off" && mode != "late_enable")
				if err := s.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
					t.Fatal(err)
				}
				recorder := &paymentInlineRecorder{}
				*s.Mailer() = recorder
				if mode == "late_enable" {
					flags[internalmail.PaymentFailedOutboxEnableEnv] = "1"
				}
				var inject, remove string
				switch mode {
				case "intent_failure":
					inject = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT reject_payment_intent CHECK (template<>'payment_failed')"
					remove = "ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT reject_payment_intent"
				case "status_failure":
					inject = "ALTER TABLE accounts ADD CONSTRAINT reject_payment_status CHECK (status<>'past_due')"
					remove = "ALTER TABLE accounts DROP CONSTRAINT reject_payment_status"
				case "receipt_failure":
					inject = "ALTER TABLE billing_payment_failure_receipts ADD CONSTRAINT reject_payment_receipt CHECK (false)"
					remove = "ALTER TABLE billing_payment_failure_receipts DROP CONSTRAINT reject_payment_receipt"
				}
				if inject != "" {
					if _, err := pool.Exec(t.Context(), inject); err != nil {
						t.Fatal(err)
					}
				}
				eventID := "event-" + uuid.NewString()
				if mode == "missing_id" {
					eventID = ""
				}
				r := paymentWebhook(t, s.Handler(), provider, customer, eventID, false, mode != "bad_signature")
				want := http.StatusServiceUnavailable
				if mode == "off" || mode == "atomic" {
					want = http.StatusOK
				} else if mode == "bad_signature" {
					want = http.StatusBadRequest
				}
				if r.Code != want {
					t.Fatal("unexpected signed webhook result", r.Code, r.Body.String())
				}
				var status string
				var intents, receipts, legacyClaims int
				read := func() {
					t.Helper()
					if err := pool.QueryRow(t.Context(), "SELECT status,(SELECT count(*) FROM platform_mail_producer_outbox),(SELECT count(*) FROM billing_payment_failure_receipts),(SELECT count(*) FROM webhook_deliveries) FROM accounts WHERE id=$1", id).Scan(&status, &intents, &receipts, &legacyClaims); err != nil {
						t.Fatal(err)
					}
				}
				read()
				if mode == "off" {
					wantMail := int32(1)
					if provider == "polar" {
						wantMail = 0 // Legacy subscription.past_due has no inline notice.
					}
					if status != "past_due" || intents != 0 || receipts != 0 || legacyClaims != 1 || recorder.calls.Load() != wantMail {
						t.Fatal("flag-off behavior changed", status, intents, receipts, legacyClaims)
					}
					return
				}
				if mode != "atomic" && (status != "active" || intents != 0 || receipts != 0) {
					t.Fatal("failed webhook partially committed status, intent or receipt")
				}
				if legacyClaims != 0 || recorder.calls.Load() != 0 {
					t.Fatal("selected outbox path took an expiring claim or sent inline")
				}
				if remove != "" {
					if _, err := pool.Exec(t.Context(), remove); err != nil {
						t.Fatal(err)
					}
					if r := paymentWebhook(t, s.Handler(), provider, customer, eventID, false, true); r.Code != http.StatusOK {
						t.Fatal("same delivery could not retry after rollback", r.Code)
					}
				} else if mode != "atomic" {
					return
				}
				for range 2 {
					if r := paymentWebhook(t, s.Handler(), provider, customer, eventID, false, true); r.Code != http.StatusOK {
						t.Fatal("replay failed", r.Code)
					}
				}
				read()
				if status != "past_due" || intents != 1 || receipts != 1 {
					t.Fatal("duplicate provider event changed dunning or intent count")
				}
				if r := paymentWebhook(t, s.Handler(), provider, customer, uuid.NewString(), true, true); r.Code != http.StatusOK {
					t.Fatal("payment recovery failed", r.Code, r.Body.String())
				}
				var intentState string
				if err := pool.QueryRow(t.Context(), "SELECT state FROM platform_mail_producer_outbox WHERE account_id=$1", id).Scan(&intentState); err != nil || intentState != "cancelled" {
					t.Fatal("payment recovery retained an unsent warning", intentState, err)
				}
				if _, err := pool.Exec(t.Context(), "UPDATE billing_payment_failure_receipts SET processed_at=clock_timestamp()-interval '30 days'"); err != nil {
					t.Fatal(err)
				}
				if r := paymentWebhook(t, s.Handler(), provider, customer, eventID, false, true); r.Code != http.StatusOK {
					t.Fatal("permanent replay rejected", r.Code)
				}
				read()
				if status != "active" || intents != 1 || receipts != 1 {
					t.Fatal("failure replay re-entered dunning after recovery")
				}
			})
		}
	}
}

func TestPaymentFailedOutboxRecoveryReviewsCurrentStatus(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	s, store, pool := *e.S(), *e.Store(), *e.Pool()
	id, customer := e.Acct().ID, "customer-"+uuid.NewString()
	if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: id, Provider: "stripe", CustomerID: customer}); err != nil {
		t.Fatal(err)
	}
	flags := paymentFlags(t, true)
	if err := s.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
		t.Fatal(err)
	}
	*s.StripeWebhookSecret() = paymentWebhookSecret
	*s.Mailer() = &paymentInlineRecorder{}
	stale := *e.Acct() // This is the snapshot a racing payment-success request read.
	if r := paymentWebhook(t, s.Handler(), "stripe", customer, uuid.NewString(), false, true); r.Code != http.StatusOK {
		t.Fatal(r.Code, r.Body.String())
	}
	if err := s.HandleBillingEventWithOptions(t.Context(), billing.Event{Type: billing.EventPaymentSucceeded, CustomerID: customer}, stale, false); err != nil {
		t.Fatal(err)
	}
	var status, intentState string
	if err := pool.QueryRow(t.Context(), "SELECT a.status,o.state FROM accounts a JOIN platform_mail_producer_outbox o ON o.account_id=a.id WHERE a.id=$1", id).Scan(&status, &intentState); err != nil || status != "active" || intentState != "cancelled" {
		t.Fatal("stale ingress snapshot missed payment recovery", status, intentState, err)
	}
}
