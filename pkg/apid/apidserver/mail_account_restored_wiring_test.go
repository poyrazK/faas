//go:build !no_pg

package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

type restoredInlineRecorder struct {
	calls atomic.Int32
	sent  chan struct{}
}

func TestAccountRestoredOutboxPreservesPolarInvoiceEnrichment(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/orders/order-recovery/invoice" {
			t.Error("unexpected provider request", r.Method, r.URL.Path)
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	defer close(release)
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	s, store, pool := *e.S(), *e.Store(), *e.Pool()
	provider, err := polar.NewProvider(polar.Config{APIKey: "private-offline-recovery-test-token", WebhookSecret: paymentWebhookSecret, Sandbox: true, BaseURL: upstream.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.WithBillingProvider(provider)
	id, customer := e.Acct().ID, "polar-recovery-"+uuid.NewString()
	if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: id, Provider: "polar", CustomerID: customer}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDunningStep(t.Context(), id, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	flags := paymentFlags(t, false)
	flags[internalmail.AccountRestoredOutboxEnableEnv] = "1"
	if err := s.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
		t.Fatal(err)
	}
	*s.Mailer() = &paymentInlineRecorder{}
	body, err := json.Marshal(map[string]any{"type": "order.paid", "data": map[string]any{"id": "order-recovery", "customer_id": customer, "currency": "eur", "period_start": "2026-10-01T00:00:00Z", "period_end": "2026-11-01T00:00:00Z"}})
	if err != nil {
		t.Fatal(err)
	}
	when, event := time.Now(), uuid.NewString()
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/polar", bytes.NewReader(body))
	r.Header.Set("webhook-id", event)
	r.Header.Set("webhook-timestamp", strconv.FormatInt(when.Unix(), 10))
	r.Header.Set("webhook-signature", polar.SignForTest(body, paymentWebhookSecret, event, when))
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		done <- w
	}()
	select {
	case w := <-done:
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("invoice enrichment blocked webhook acknowledgement")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("durable recovery skipped Polar invoice enrichment")
	}
}

func (r *restoredInlineRecorder) Send(context.Context, apidserver.Message) error {
	r.calls.Add(1)
	select {
	case r.sent <- struct{}{}:
	default:
	}
	return nil
}

func TestAccountRestoredOutboxSignedWebhooksAndAtomicBoundaries(t *testing.T) {
	for _, provider := range []string{"stripe", "paddle", "polar"} {
		for _, mode := range []string{"off", "atomic", "intent_failure", "status_failure", "org_failure", "receipt_failure", "cancel_failure", "quota_followup_failure", "late_enable", "bad_signature", "missing_id"} {
			if provider == "polar" && mode == "missing_id" {
				continue // The Standard Webhooks verifier rejects a missing ID first.
			}
			t.Run(provider+"/"+mode, func(t *testing.T) {
				e := apidserver.SetupPGHandler(t, api.PlanPro)
				s, store, pool := *e.S(), *e.Store(), *e.Pool()
				s.WithBillingProvider(paymentProvider(t, provider))
				*s.StripeWebhookSecret() = paymentWebhookSecret
				id, customer := e.Acct().ID, "recovery-"+uuid.NewString()
				if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: id, Provider: provider, CustomerID: customer}); err != nil {
					t.Fatal(err)
				}
				flags := paymentFlags(t, true)
				if mode != "off" && mode != "late_enable" {
					flags[internalmail.AccountRestoredOutboxEnableEnv] = "1"
				}
				if err := s.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
					t.Fatal(err)
				}
				recorder := &restoredInlineRecorder{sent: make(chan struct{}, 2)}
				*s.Mailer() = recorder
				if r := paymentWebhook(t, s.Handler(), provider, customer, uuid.NewString(), false, true); r.Code != http.StatusOK {
					t.Fatal("failed to enter dunning", r.Code, r.Body.String())
				}
				if mode == "late_enable" {
					flags[internalmail.AccountRestoredOutboxEnableEnv] = "1"
				}
				var inject, remove string
				switch mode {
				case "intent_failure":
					inject = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT reject_restored_intent CHECK(template<>'account_restored')"
					remove = "ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT reject_restored_intent"
				case "status_failure":
					inject = "ALTER TABLE accounts ADD CONSTRAINT reject_restored_status CHECK(status<>'active')"
					remove = "ALTER TABLE accounts DROP CONSTRAINT reject_restored_status"
				case "org_failure":
					if _, err := pool.Exec(t.Context(), "UPDATE orgs SET status='suspended' WHERE personal_owner_account_id=$1", id); err != nil {
						t.Fatal(err)
					}
					inject = "ALTER TABLE orgs ADD CONSTRAINT reject_restored_org CHECK(status<>'active')"
					remove = "ALTER TABLE orgs DROP CONSTRAINT reject_restored_org"
				case "receipt_failure":
					inject = "ALTER TABLE billing_payment_recovery_receipts ADD CONSTRAINT reject_restored_receipt CHECK(false)"
					remove = "ALTER TABLE billing_payment_recovery_receipts DROP CONSTRAINT reject_restored_receipt"
				case "cancel_failure":
					inject = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT reject_restored_cancel CHECK(state<>'cancelled')"
					remove = "ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT reject_restored_cancel"
				case "quota_followup_failure":
					if _, err := pool.Exec(t.Context(), "UPDATE accounts SET last_quota_warning_at=clock_timestamp() WHERE id=$1", id); err != nil {
						t.Fatal(err)
					}
					inject = "ALTER TABLE accounts ADD CONSTRAINT reject_restored_quota_clear CHECK(last_quota_warning_at IS NOT NULL)"
					remove = "ALTER TABLE accounts DROP CONSTRAINT reject_restored_quota_clear"
				}
				if inject != "" {
					if _, err := pool.Exec(t.Context(), inject); err != nil {
						t.Fatal(err)
					}
				}
				eventID := uuid.NewString()
				if mode == "missing_id" {
					eventID = ""
				}
				response := paymentWebhook(t, s.Handler(), provider, customer, eventID, true, mode != "bad_signature")
				want := http.StatusServiceUnavailable
				if mode == "off" || mode == "atomic" {
					want = http.StatusOK
				} else if mode == "bad_signature" {
					want = http.StatusBadRequest
				}
				if response.Code != want {
					t.Fatal("unexpected recovery response", response.Code, response.Body.String())
				}
				var status, failedState string
				var confirmations, receipts, claims int
				read := func() {
					t.Helper()
					if err := pool.QueryRow(t.Context(), "SELECT status,(SELECT state FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='payment_failed' ORDER BY created_at LIMIT 1),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='account_restored'),(SELECT count(*) FROM billing_payment_recovery_receipts),(SELECT count(*) FROM webhook_deliveries) FROM accounts WHERE id=$1", id).Scan(&status, &failedState, &confirmations, &receipts, &claims); err != nil {
						t.Fatal(err)
					}
				}
				read()
				if mode == "off" {
					select {
					case <-recorder.sent:
					case <-time.After(time.Second):
						t.Fatal("legacy recovery notice was not sent")
					}
					if status != "active" || failedState != "cancelled" || confirmations != 0 || receipts != 0 || claims != 1 || recorder.calls.Load() != 1 {
						t.Fatal("flag-off recovery behavior changed")
					}
					return
				}
				if recorder.calls.Load() != 0 || claims != 0 {
					t.Fatal("selected recovery sent inline or took an expiring claim")
				}
				committed := mode == "atomic" || mode == "quota_followup_failure"
				if committed {
					if status != "active" || failedState != "cancelled" || confirmations != 1 || receipts != 1 {
						t.Fatal("committed recovery lost its confirmation or cancellation", status, failedState, confirmations, receipts)
					}
				} else if status != "past_due" || failedState != "queued" || confirmations != 0 || receipts != 0 {
					t.Fatal("failed recovery partially committed", status, failedState, confirmations, receipts)
				}
				if remove != "" {
					if _, err := pool.Exec(t.Context(), remove); err != nil {
						t.Fatal(err)
					}
				} else if mode != "atomic" {
					return
				}
				for range 2 {
					if r := paymentWebhook(t, s.Handler(), provider, customer, eventID, true, true); r.Code != http.StatusOK {
						t.Fatal("same delivery could not retry safely", r.Code, r.Body.String())
					}
				}
				read()
				if status != "active" || failedState != "cancelled" || confirmations != 1 || receipts != 1 || recorder.calls.Load() != 0 {
					t.Fatal("retried success duplicated recovery or confirmation")
				}
				// The same old success must not lift a fresh overdue period,
				// even after the old ingress claim would have expired.
				if r := paymentWebhook(t, s.Handler(), provider, customer, uuid.NewString(), false, true); r.Code != http.StatusOK {
					t.Fatal(r.Code, r.Body.String())
				}
				if _, err := pool.Exec(t.Context(), "UPDATE billing_payment_recovery_receipts SET processed_at=clock_timestamp()-interval '30 days'"); err != nil {
					t.Fatal(err)
				}
				if r := paymentWebhook(t, s.Handler(), provider, customer, eventID, true, true); r.Code != http.StatusOK {
					t.Fatal(r.Code, r.Body.String())
				}
				read()
				if status != "past_due" || confirmations != 1 || receipts != 1 {
					t.Fatal("old success restored a later failure")
				}
			})
		}
	}
}

func TestAccountRestoredOutboxIndependentFlagAndCurrentStatus(t *testing.T) {
	for _, mode := range []string{"past_due", "dunning_suspended", "dunning_deleted", "operator_suspended", "active"} {
		t.Run(mode, func(t *testing.T) {
			e := apidserver.SetupPGHandler(t, api.PlanPro)
			s, store, pool := *e.S(), *e.Store(), *e.Pool()
			id, customer := e.Acct().ID, "recovery-"+uuid.NewString()
			if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: id, Provider: "stripe", CustomerID: customer}); err != nil {
				t.Fatal(err)
			}
			flags := paymentFlags(t, false) // Failure issuance is independently off.
			flags[internalmail.AccountRestoredOutboxEnableEnv] = "1"
			if err := s.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			recorder := &restoredInlineRecorder{sent: make(chan struct{}, 1)}
			*s.Mailer() = recorder
			stale := *e.Acct() // A racing success request read active before failure.
			if mode != "active" && mode != "operator_suspended" {
				if err := store.MarkDunningStep(t.Context(), id, state.AccountActive, state.AccountPastDue); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "dunning_suspended" || mode == "dunning_deleted" {
				if err := store.MarkDunningStep(t.Context(), id, state.AccountPastDue, state.AccountSuspended); err != nil {
					t.Fatal(err)
				}
				if mode == "dunning_deleted" {
					if err := store.MarkDunningStep(t.Context(), id, state.AccountSuspended, state.AccountDeletedPending); err != nil {
						t.Fatal(err)
					}
				}
			} else if mode == "operator_suspended" {
				if err := store.UpdateAccountStatus(t.Context(), id, state.AccountSuspended); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.HandleBillingEventWithOptions(t.Context(), billing.Event{Type: billing.EventPaymentSucceeded, CustomerID: customer, EventID: uuid.NewString()}, stale, false); err != nil {
				t.Fatal(err)
			}
			var status string
			var confirmations int
			if err := pool.QueryRow(t.Context(), "SELECT status,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='account_restored') FROM accounts WHERE id=$1", id).Scan(&status, &confirmations); err != nil {
				t.Fatal(err)
			}
			wantStatus, wantNotices := "active", 1
			if mode == "active" || mode == "operator_suspended" {
				wantNotices = 0
			}
			if mode == "operator_suspended" {
				wantStatus = "suspended"
			}
			if status != wantStatus || confirmations != wantNotices || recorder.calls.Load() != 0 {
				t.Fatal("recovery missed current state or required the failure flag", status, confirmations)
			}
		})
	}
}
