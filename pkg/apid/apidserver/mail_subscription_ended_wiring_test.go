//go:build !no_pg

package apidserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

func endedWebhook(t *testing.T, h http.Handler, provider, customer, event, sub string, valid bool) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{"event_id": event, "event_type": "subscription.canceled", "data": map[string]any{"id": sub, "customer_id": customer}}
	if provider == "stripe" {
		payload = map[string]any{"id": event, "type": "customer.subscription.deleted", "data": map[string]any{"object": map[string]any{"id": sub, "customer": customer}}}
	}
	if provider == "polar" {
		payload = map[string]any{"type": "subscription.revoked", "data": map[string]any{"id": sub, "customer_id": customer, "status": "canceled"}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/"+provider, bytes.NewReader(body))
	when, secret := time.Now(), paymentWebhookSecret
	if !valid {
		secret = "wrong-end-webhook-secret"
	}
	switch provider {
	case "stripe":
		r.Header.Set("Stripe-Signature", stripe.SignForTest(body, secret, when))
	case "paddle":
		r.Header.Set("Paddle-Signature", paddle.SignForTestForTest(body, secret, when))
	case "polar":
		r.Header.Set("webhook-id", event)
		r.Header.Set("webhook-timestamp", strconv.FormatInt(when.Unix(), 10))
		r.Header.Set("webhook-signature", polar.SignForTest(body, secret, event, when))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSubscriptionEndedOutboxSignedWebhooksAndAtomicBoundaries(t *testing.T) {
	for _, provider := range []string{"stripe", "paddle", "polar"} {
		for _, mode := range []string{"off", "atomic", "intent_failure", "plan_failure", "binding_failure", "org_failure", "streaming_failure", "receipt_failure", "business_failure", "late_enable", "bad_signature", "missing_id", "missing_subscription"} {
			if provider == "polar" && mode == "missing_id" {
				continue
			}
			t.Run(provider+"/"+mode, func(t *testing.T) {
				e := apidserver.SetupPGHandler(t, api.PlanPro)
				s, store, pool := *e.S(), *e.Store(), *e.Pool()
				s.WithBillingProvider(paymentProvider(t, provider))
				*s.StripeWebhookSecret() = paymentWebhookSecret
				id, customer, sub := e.Acct().ID, "ended-customer-"+uuid.NewString(), "sub-"+uuid.NewString()
				if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: id, Provider: provider, CustomerID: customer, SubscriptionID: sub}); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), "UPDATE accounts SET provider_customer_id=$2,stripe_subscription_item=$3 WHERE id=$1", id, customer, sub); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), "UPDATE orgs SET provider_customer_id=$2,stripe_subscription_item=$3 WHERE personal_owner_account_id=$1", id, customer, sub); err != nil {
					t.Fatal(err)
				}
				app, err := store.CreateApp(t.Context(), state.App{AccountID: id, Slug: "ended-app", Type: state.AppTypeApp})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), "UPDATE apps SET streaming_enabled=true WHERE id=$1", app.ID); err != nil {
					t.Fatal(err)
				}
				flags := paymentFlags(t, false)
				if mode != "off" && mode != "late_enable" {
					flags[internalmail.SubscriptionEndedOutboxEnableEnv] = "1"
				}
				if err := s.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
					t.Fatal(err)
				}
				recorder := &restoredInlineRecorder{sent: make(chan struct{}, 2)}
				*s.Mailer() = recorder
				if mode == "late_enable" {
					flags[internalmail.SubscriptionEndedOutboxEnableEnv] = "1"
				}
				var table, constraint, predicate string
				switch mode {
				case "intent_failure":
					table, constraint, predicate = "platform_mail_producer_outbox", "reject_ended_intent", "template<>'subscription_ended'"
				case "plan_failure":
					table, constraint, predicate = "accounts", "reject_ended_plan", fmt.Sprintf("id<>'%s'::uuid OR plan<>'free'", id)
				case "binding_failure":
					table, constraint, predicate = "billing_identities", "reject_ended_binding", fmt.Sprintf("account_id<>'%s'::uuid OR subscription_id<>''", id)
				case "org_failure":
					table, constraint, predicate = "orgs", "reject_ended_org", fmt.Sprintf("personal_owner_account_id<>'%s'::uuid OR plan<>'free'", id)
				case "streaming_failure":
					table, constraint, predicate = "apps", "reject_ended_stream", fmt.Sprintf("id<>'%s'::uuid OR streaming_enabled", app.ID)
				case "receipt_failure":
					table, constraint, predicate = "billing_subscription_end_receipts", "reject_ended_receipt", "false"
				case "business_failure":
					table, constraint, predicate = "billing_subscription_ends", "reject_ended_business", "false"
				}
				if table != "" {
					if _, err := pool.Exec(t.Context(), "ALTER TABLE "+table+" ADD CONSTRAINT "+constraint+" CHECK("+predicate+")"); err != nil {
						t.Fatal(err)
					}
				}
				event, requestedSub := uuid.NewString(), sub
				if mode == "missing_id" {
					event = ""
				}
				if mode == "missing_subscription" {
					requestedSub = ""
				}
				w := endedWebhook(t, s.Handler(), provider, customer, event, requestedSub, mode != "bad_signature")
				want := http.StatusServiceUnavailable
				if mode == "off" || mode == "atomic" {
					want = http.StatusOK
				}
				if mode == "bad_signature" {
					want = http.StatusBadRequest
				}
				if w.Code != want {
					t.Fatal(w.Code, w.Body.String())
				}
				var plan, status, binding, cache, orgPlan string
				var streaming bool
				var intents, receipts, ends, claims int
				read := func() {
					t.Helper()
					if err := pool.QueryRow(t.Context(), `SELECT a.plan,a.status,b.subscription_id,a.stripe_subscription_item,g.plan,p.streaming_enabled,
(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='subscription_ended'),(SELECT count(*) FROM billing_subscription_end_receipts),(SELECT count(*) FROM billing_subscription_ends),(SELECT count(*) FROM webhook_deliveries)
FROM accounts a JOIN billing_identities b ON b.account_id=a.id AND b.provider=$2 JOIN orgs g ON g.personal_owner_account_id=a.id JOIN apps p ON p.id=$3 WHERE a.id=$1`, id, provider, app.ID).Scan(&plan, &status, &binding, &cache, &orgPlan, &streaming, &intents, &receipts, &ends, &claims); err != nil {
						t.Fatal(err)
					}
				}
				read()
				if mode == "off" {
					select {
					case <-recorder.sent:
					case <-time.After(5 * time.Second):
						t.Fatal("legacy confirmation missing")
					}
					if plan != "free" || status != "active" || binding != "" || cache != "" || orgPlan != "free" || streaming || intents != 0 || receipts != 0 || ends != 0 || claims != 1 || recorder.calls.Load() != 1 {
						t.Fatal("flag-off behavior changed", plan, status, binding, cache, orgPlan, streaming, intents, receipts, ends, claims)
					}
					return
				}
				if mode != "atomic" && (plan != "pro" || status != "active" || binding != sub || cache != sub || orgPlan != "pro" || !streaming || intents != 0 || receipts != 0 || ends != 0) {
					t.Fatal("failed webhook partially committed termination", plan, binding, cache, orgPlan, streaming, intents, receipts, ends)
				}
				if claims != 0 || recorder.calls.Load() != 0 {
					t.Fatal("selected path claimed legacy delivery or sent inline")
				}
				if table != "" {
					if _, err := pool.Exec(t.Context(), "ALTER TABLE "+table+" DROP CONSTRAINT "+constraint); err != nil {
						t.Fatal(err)
					}
					if retry := endedWebhook(t, s.Handler(), provider, customer, event, sub, true); retry.Code != http.StatusOK {
						t.Fatal("same failed delivery was consumed", retry.Code, retry.Body.String())
					}
				} else if mode != "atomic" {
					return
				}
				for range 2 {
					if replay := endedWebhook(t, s.Handler(), provider, customer, event, sub, true); replay.Code != http.StatusOK {
						t.Fatal(replay.Code, replay.Body.String())
					}
				}
				read()
				if plan != "free" || status != "active" || binding != "" || cache != "" || orgPlan != "free" || streaming || intents != 1 || receipts != 1 || ends != 1 || claims != 0 {
					t.Fatal("termination not atomic or replay duplicated notice", plan, status, binding, cache, orgPlan, streaming, intents, receipts, ends, claims)
				}
			})
		}
	}
}

func TestSubscriptionEndedOutboxReplaysCannotDowngradeNewSubscription(t *testing.T) {
	for _, provider := range []string{"stripe", "paddle", "polar"} {
		t.Run(provider, func(t *testing.T) {
			e := apidserver.SetupPGHandler(t, api.PlanPro)
			s, store, pool := *e.S(), *e.Store(), *e.Pool()
			s.WithBillingProvider(paymentProvider(t, provider))
			*s.StripeWebhookSecret() = paymentWebhookSecret
			id, customer, sub, event := e.Acct().ID, "rebind-"+uuid.NewString(), "original-sub", uuid.NewString()
			if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: id, Provider: provider, CustomerID: customer, SubscriptionID: sub}); err != nil {
				t.Fatal(err)
			}
			flags := paymentFlags(t, false)
			flags[internalmail.SubscriptionEndedOutboxEnableEnv] = "1"
			if err := s.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			recorder := &paymentInlineRecorder{}
			*s.Mailer() = recorder
			if first := endedWebhook(t, s.Handler(), provider, customer, event, sub, true); first.Code != http.StatusOK {
				t.Fatal(first.Code, first.Body.String())
			}
			if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: id, Provider: provider, CustomerID: customer, SubscriptionID: "new-sub"}); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateAccountPlan(t.Context(), id, api.PlanScale); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), "UPDATE accounts SET stripe_subscription_item='new-sub' WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), "UPDATE billing_subscription_end_receipts SET processed_at=clock_timestamp()-interval '30 days'"); err != nil {
				t.Fatal(err)
			}
			for _, delivery := range []string{event, uuid.NewString()} {
				if w := endedWebhook(t, s.Handler(), provider, customer, delivery, sub, true); w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			var plan, binding string
			var intents int
			if err := pool.QueryRow(t.Context(), "SELECT a.plan,b.subscription_id,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='subscription_ended') FROM accounts a JOIN billing_identities b ON b.account_id=a.id AND b.provider=$2 WHERE a.id=$1", id, provider).Scan(&plan, &binding, &intents); err != nil || plan != "scale" || binding != "new-sub" || intents != 1 || recorder.calls.Load() != 0 {
				t.Fatal("late cancellation rewound subscription", plan, binding, intents, err)
			}
			// The shared billing dispatcher also uses the current locked row,
			// including when its caller's account snapshot predates a change.
			if err := s.HandleBillingEventWithOptions(t.Context(), billing.Event{Type: billing.EventSubscriptionCanceled, EventID: uuid.NewString(), CustomerID: customer, SubscriptionID: "new-sub"}, *e.Acct(), false); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(t.Context(), "SELECT plan FROM accounts WHERE id=$1", id).Scan(&plan); err != nil || plan != "free" {
				t.Fatal(plan, err)
			}
		})
	}
}
