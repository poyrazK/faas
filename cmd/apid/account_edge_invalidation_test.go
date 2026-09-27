package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// gatewayd caches each app's account status and plan with no TTL and
// re-reads them only on app_changed. These paths changed account status or
// plan without emitting it, so the edge kept answering from the old state:
// a customer who paid after a suspension stayed behind a 402 until
// gatewayd restarted.

func edgeInvalidationEnv(t *testing.T) (*server, *state.MemStore, *spyNotifier, state.Account, string) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "edge@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, acct.ID, "cus_test_123"); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "edge-app", Runtime: "node22", RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	spy := &spyNotifier{}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", spy,
		stripeWebhookSecretForTest, noopMailer{}, stubGithubdClient{}, nil, nil, 0, "")
	return srv, store, spy, acct, app.ID
}

func appChangedFor(spy *spyNotifier, appID, kind string) bool {
	for _, n := range spy.snapshot() {
		if n.Channel != db.NotifyAppChanged {
			continue
		}
		p, err := db.ParseAppChangedPayload(n.Payload)
		if err == nil && p.AppID == appID && p.Kind == kind {
			return true
		}
	}
	return false
}

func TestPaymentSucceeded_TellsTheEdge(t *testing.T) {
	srv, store, spy, acct, appID := edgeInvalidationEnv(t)
	ctx := context.Background()
	for _, step := range [][2]state.AccountStatus{{state.AccountActive, state.AccountPastDue}, {state.AccountPastDue, state.AccountSuspended}} {
		if err := store.MarkDunningStep(ctx, acct.ID, step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	if rec := postStripeEvent(t, srv.handler(), "invoice.payment_succeeded", "cus_test_123"); rec.Code != http.StatusOK {
		t.Fatalf("webhook = %d %s", rec.Code, rec.Body)
	}
	if !appChangedFor(spy, appID, "account_reactivated") {
		t.Fatalf("payment restored the account without app_changed(account_reactivated); notifications: %v", spy.snapshot())
	}
}

func TestSelfServiceDeleteAndRestore_TellTheEdge(t *testing.T) {
	srv, store, spy, acct, appID := edgeInvalidationEnv(t)
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	h := srv.handler()
	do := func(method, path string) {
		t.Helper()
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(""))
		r.Header.Set("Authorization", "Bearer "+pt)
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d %s", method, path, rec.Code, rec.Body)
		}
	}
	do("DELETE", "/v1/account")
	if !appChangedFor(spy, appID, "account_deleted_pending") {
		t.Fatalf("deletion scheduled without app_changed(account_deleted_pending)")
	}
	do("POST", "/v1/account/restore")
	if !appChangedFor(spy, appID, "account_reactivated") {
		t.Fatalf("restore without app_changed(account_reactivated)")
	}
}

func TestSetAccountPlan_TellsTheEdgeOnlyOnChange(t *testing.T) {
	srv, _, spy, acct, appID := edgeInvalidationEnv(t)
	ctx := context.Background()
	if err := srv.setAccountPlan(ctx, acct, api.PlanHobby); err != nil { // unchanged
		t.Fatal(err)
	}
	if appChangedFor(spy, appID, "account_plan_changed") {
		t.Fatal("an unchanged plan fanned out app_changed")
	}
	if err := srv.setAccountPlan(ctx, acct, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if !appChangedFor(spy, appID, "account_plan_changed") {
		t.Fatal("plan change without app_changed(account_plan_changed)")
	}
}
